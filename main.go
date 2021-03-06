package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"

	"github.com/apex/log"
	jsonlog "github.com/apex/log/handlers/json"
	"github.com/apex/log/handlers/text"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/endpoints"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/pkg/errors"
)

type Sender struct {
	cloudwatchSvc *cloudwatch.Client
}

type infoPayload struct {
	ControlLogin    string `json:"control_login"`
	ControlPassword string `json:"control_password"`
}

type Info struct {
	ID             string `json:"ID"`
	Login          string `json:"login"`
	Postcode       string `json:"postcode"`
	TxRate         string `json:"tx_rate"`
	RxRate         string `json:"rx_rate"`
	TxRateAdjusted string `json:"tx_rate_adjusted"`
	QuotaMonthly   string `json:"quota_monthly"`
	QuotaRemaining string `json:"quota_remaining"`
	QuotaTimestamp string `json:"quota_timestamp"`
}

type infoResponse struct {
	Subsystem string `json:"subsystem"`
	Command   string `json:"command"`
	Request   struct {
		ControlLogin    string `json:"control_login"`
		ControlPassword string `json:"control_password"`
		Service         string `json:"service"`
	} `json:"request"`
	Options []struct {
		Title  string `json:"title"`
		Option []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Title   string `json:"title"`
			Size    string `json:"size"`
			Recheck string `json:"recheck"`
			Value   string `json:"value"`
		} `json:"option,omitempty"`
	} `json:"options"`
	Infos []Info `json:"info"`
	Error string `json:"error"`
}

func main() {
	if os.Getenv("UP_STAGE") == "" {
		log.SetHandler(text.Default)
	} else {
		log.SetHandler(jsonlog.Default)
	}

	http.HandleFunc("/favicon.ico", http.NotFound)
	http.HandleFunc("/", get)
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		log.Fatalf("error listening: %s", err)
	}
}

func aainfo() (info Info, err error) {
	u := infoPayload{ControlLogin: os.Getenv("LOGIN"), ControlPassword: os.Getenv("PASSWORD")}
	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(u)
	resp, err := http.Post("https://chaos2.aa.net.uk/broadband/info", "application/json; charset=utf-8", b)
	if err != nil {
		return info, errors.Wrap(err, "failed to make POST request")
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return info, errors.Wrap(err, "failed to read A&A's response body")
	}

	if resp.StatusCode != 200 {
		// 200 is returned even on failed auth btw
		return info, errors.Wrap(err, "failed to retrieve Internet speed")
	}

	var infor infoResponse
	err = json.Unmarshal(body, &infor)
	if len(infor.Infos) == 0 {
		return info, errors.Wrap(err, "failed to get response")
	}
	return infor.Infos[0], err
}

func get(w http.ResponseWriter, r *http.Request) {

	info, err := aainfo()

	if err != nil {
		log.WithError(err).Error("unable to retrieve aainfo")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// log.Infof("%+v", info)

	cw, err := NewSender()
	if err != nil {
		log.WithError(err).Error("failed to setup CW sender")
	}

	err = cw.log(info)
	if err != nil {
		log.WithError(err).Error("failed to log CW sender")
	}

	upload, err := cw.base64image("upload")
	if err != nil {
		log.WithError(err).Fatal("failed to retrieve CW image")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	download, err := cw.base64image("download")
	if err != nil {
		log.WithError(err).Fatal("failed to retrieve CW image")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.WithFields(log.Fields{
		"upload(rx)":     info.RxRate,
		"download(tx)":   info.TxRate,
		"TxRateAdjusted": info.TxRateAdjusted,
	}).Info("info")

	t := template.Must(template.New("").Funcs(template.FuncMap{"formatRate": formatRate, "formatQuota": formatQuota}).ParseFiles("index.html"))

	err = t.ExecuteTemplate(w, "index.html", struct {
		Info          Info
		UploadImage   string
		DownloadImage string
	}{info, upload, download})

	if err != nil {
		log.WithError(err).Error("Unable to print template")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func NewSender() (*Sender, error) {
	cfg, err := external.LoadDefaultAWSConfig(external.WithSharedConfigProfile("mine"))
	if err != nil {
		return nil, fmt.Errorf("could not create AWS session: %s", err)
	}
	cfg.Region = endpoints.ApSoutheast1RegionID
	return &Sender{cloudwatchSvc: cloudwatch.New(cfg)}, nil
}

func (sender *Sender) log(i Info) error {
	upload, err := strconv.ParseFloat(i.RxRate, 64)
	if err != nil {
		return errors.Wrap(err, "failed to parse float, missing value?")
	}

	download, err := strconv.ParseFloat(i.TxRate, 64)
	if err != nil {
		return errors.Wrap(err, "failed to parse float, missing value?")
	}

	req := sender.cloudwatchSvc.PutMetricDataRequest(&cloudwatch.PutMetricDataInput{
		MetricData: []cloudwatch.MetricDatum{
			cloudwatch.MetricDatum{
				MetricName: aws.String("upload"),
				Unit:       cloudwatch.StandardUnitBitsSecond,
				Value:      aws.Float64(upload),
				Dimensions: []cloudwatch.Dimension{},
			},
			cloudwatch.MetricDatum{
				MetricName: aws.String("download"),
				Unit:       cloudwatch.StandardUnitBitsSecond,
				Value:      aws.Float64(download),
				Dimensions: []cloudwatch.Dimension{},
			},
		},
		Namespace: aws.String("prazespeed"),
	})
	_, err = req.Send(context.TODO())
	return err
}

func (sender *Sender) base64image(metric string) (string, error) {
	// https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/CloudWatch-Metric-Widget-Structure.html
	// Tip: Look at source of Cloudwatch Metric graph in the console

	log.WithField("metric", metric).Info("Creating plot")

	plot := cloudwatch.GetMetricWidgetImageInput{
		MetricWidget: aws.String(fmt.Sprintf(`{ "metrics":
		[
		[ "prazespeed", "%s", { "period": 3600, "stat": "Minimum" } ]
		],
	  "yAxis": { "left": { "min": 0 }},
	  "start": "-P10M",
	  "title": "Superfast Cornwall %s speeds 21CN FTTC over 10 months"}`, metric, metric)),
	}
	err := plot.Validate()
	if err != nil {
		return "", errors.Wrap(err, "failed validating metric request")
	}

	req := sender.cloudwatchSvc.GetMetricWidgetImageRequest(&plot)

	image, err := req.Send(context.TODO())
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(image.MetricWidgetImage), err

}

func formatRate(num string) string {
	i, _ := strconv.ParseFloat(num, 64)
	return fmt.Sprintf("%.2f Mb/s", i/1000/1000)
}

func formatQuota(num string) string {
	i, _ := strconv.ParseInt(num, 10, 64)
	return fmt.Sprintf("%d GB", i/1000/1000/1000)
}
