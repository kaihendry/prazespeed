package main

import (
	"bytes"
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

type infoPayload struct {
	ControlLogin    string `json:"control_login"`
	ControlPassword string `json:"control_password"`
	Service         string `json:"service"`
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

func init() {
	if os.Getenv("UP_STAGE") == "" {
		log.SetHandler(text.Default)
	} else {
		log.SetHandler(jsonlog.Default)
	}
}

func main() {
	http.HandleFunc("/favicon.ico", http.NotFound)
	http.HandleFunc("/", get)
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		log.Fatalf("error listening: %s", err)
	}
}

func aainfo() (info Info, err error) {
	u := infoPayload{ControlLogin: os.Getenv("LOGIN"), ControlPassword: os.Getenv("PASSWORD"), Service: os.Getenv("NUMBER")}
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
		return info, errors.Wrap(err, "failed to retrieve Internet speed")
	}

	var infor infoResponse
	err = json.Unmarshal(body, &infor)
	return infor.Infos[0], err

}

func get(w http.ResponseWriter, r *http.Request) {

	info, err := aainfo()

	if err != nil {
		log.WithError(err).Error("unable to retrieve aainfo")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cw, err := NewSender()
	if err != nil {
		log.WithError(err).Error("failed to setup CW sender")
	}

	err = cw.log(info)
	if err != nil {
		log.WithError(err).Error("failed to log CW sender")
	}

	image, err := cw.image()
	if err != nil {
		log.WithError(err).Error("failed to retrieve CW image")
	} else {
		log.Info(image)
	}

	log.WithFields(log.Fields{
		"upload(rx)":     info.RxRate,
		"download(tx)":   info.TxRate,
		"TxRateAdjusted": info.TxRateAdjusted,
	}).Info("info")

	t := template.Must(template.New("").Funcs(template.FuncMap{"formatRate": formatRate, "formatQuota": formatQuota}).ParseFiles("index.html"))

	err = t.ExecuteTemplate(w, "index.html", struct {
		Info  Info
		Image string
	}{info, image})

	if err != nil {
		log.WithError(err).Error("Unable to print template")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type Sender struct {
	cloudwatchSvc *cloudwatch.CloudWatch
}

func NewSender() (*Sender, error) {
	cfg, err := external.LoadDefaultAWSConfig(external.WithSharedConfigProfile("mine"))
	if err != nil {
		return nil, fmt.Errorf("could not create AWS session: %s", err)
	}
	cfg.Region = endpoints.ApSoutheast1RegionID
	return &Sender{cloudwatch.New(cfg)}, nil
}

func (sender *Sender) log(i Info) error {
	upload, err := strconv.ParseFloat(i.RxRate, 64)
	if err != nil {
		return err
	}

	download, err := strconv.ParseFloat(i.TxRate, 64)
	if err != nil {
		return err
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
	_, err = req.Send()
	return err
}

func (sender *Sender) image() (string, error) {
	req := sender.cloudwatchSvc.GetMetricWidgetImageRequest(&cloudwatch.GetMetricWidgetImageInput{
		// https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/CloudWatch-Metric-Widget-Structure.html
		MetricWidget: aws.String(`{ "metrics": [[ "prazespeed", "download" ], [ "prazespeed", "upload" ]] }`),
	})
	image, err := req.Send()
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
