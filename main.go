package main

import (
	"bytes"
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
	"github.com/gorilla/pat"
)

type InfoPayload struct {
	ControlLogin    string `json:"control_login"`
	ControlPassword string `json:"control_password"`
	Service         string `json:"service"`
}

type InfoResponse struct {
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
	Info []struct {
		ID             string `json:"ID"`
		Login          string `json:"login"`
		Postcode       string `json:"postcode"`
		TxRate         string `json:"tx_rate"`
		RxRate         string `json:"rx_rate"`
		TxRateAdjusted string `json:"tx_rate_adjusted"`
		QuotaMonthly   string `json:"quota_monthly"`
		QuotaRemaining string `json:"quota_remaining"`
		QuotaTimestamp string `json:"quota_timestamp"`
	} `json:"info"`
	Error string `json:"error"`
}

// use JSON logging when run by Up (including `up start`).
func init() {
	if os.Getenv("UP_STAGE") == "" {
		log.SetHandler(text.Default)
	} else {
		log.SetHandler(jsonlog.Default)
	}
}

// setup.
func main() {
	addr := ":" + os.Getenv("PORT")
	app := pat.New()
	app.Get("/", get)
	if err := http.ListenAndServe(addr, app); err != nil {
		log.WithError(err).Fatal("error listening")
	}
}

func get(w http.ResponseWriter, r *http.Request) {

	u := InfoPayload{ControlLogin: os.GenEnv("LOGIN"), ControlPassword: os.Getenv("PASSWORD"), Service: os.Getenv("NUMBER")}
	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(u)
	resp, err := http.Post("https://chaos2.aa.net.uk/broadband/info", "application/json; charset=utf-8", b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var infor InfoResponse
	err = json.Unmarshal(body, &infor)

	log.WithFields(log.Fields{
		"response":  string(body),
		"line info": infor.Info[0],
		"rxrate":    infor.Info[0].RxRate,
	}).Info("res")

	t := template.New("medialist")

	template.Must(t.Funcs(template.FuncMap{"formatRate": formatRate, "formatQuota": formatQuota}).Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8" />
<body>
<h1>Download {{ .TxRate | formatRate }}</h1>
<h1>Upload {{ .RxRate | formatRate }}</h1>
<h1>Quota Remaining {{ .QuotaRemaining | formatQuota }} / Monthly {{ .QuotaMonthly | formatQuota }}</h1>

<a href="http://github.com/kaihendry/prazespeed">Source code</a>

</body>
</html>`))

	err = t.Execute(w, infor.Info[0])

	if err != nil {
		log.WithError(err).Fatal("Unable to print template")
	}

}

func formatRate(num string) string {
	i, _ := strconv.ParseFloat(num, 64)
	return fmt.Sprintf("%.2f Mb/s", i/1000/1000)
}

func formatQuota(num string) string {
	i, _ := strconv.ParseInt(num, 10, 64)
	return fmt.Sprintf("%d GB", i/1000/1000/1000)
}
