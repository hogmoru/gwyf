package gwyf

import (
	"appengine"
	"appengine/urlfetch"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"text/template"
	"time"
)

const (
	wapUrl = "http://wap.ratp.fr/siv/schedule?service=next" +
		`&reseau={{.Mode}}&lineid={{.Line}}&directionsens={{.Direction}}&stationname={{.Station}}"`
	schedResultPattern = `&gt;&nbsp;([^<]+)</div>.*>(\w+)</a>.*<div class="schmsg."><b>([^<]+)</b>`
)

var (
	delegateUrlTemplate = template.Must(template.New("schedUrl").Parse(wapUrl))
	schedResultRegexp   = regexp.MustCompile(schedResultPattern)
)

type jsonResult struct {
	DelegateDuration float64 `json:"duration_s"`
	IncomingTrains   []train `json:"incoming_trains"`
}

type train struct {
	Destination string `json:"destination"`
	Mission     string `json:"mission"`
	Stop        string `json:"stop"`
}

func init() {
	http.HandleFunc("/", errorHandler(worker))
}

func errorHandler(f func(http.ResponseWriter, appengine.Context, *http.Request) error) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		context := appengine.NewContext(request)
		err := f(writer, context, request)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			context.Errorf(err.Error())
		}
	}
}

func worker(writer http.ResponseWriter, context appengine.Context, request *http.Request) error {
	delegateURL, err := buildDelegateURL(request)
	if err != nil {
		return fmt.Errorf("Failed to build delegate URL: %v", err)
	}
	startTime := time.Now()
	delegateResult, err := queryDelegateBackend(context, delegateURL)
	if err != nil {
		return fmt.Errorf("Failed to query RATP service: %v", err)
	}
	delegateDuration := time.Since(startTime).Seconds()
	trains, err := parseDelegateResult(delegateResult)
	if err != nil {
		return fmt.Errorf("Failed to parse output of RATP service: %v", err)
	}
	marshal := mkMarshalFunc(request.URL.Query()["pretty"] != nil)
	jsonTrains, err := marshal(jsonResult{IncomingTrains: trains, DelegateDuration: delegateDuration})
	if err != nil {
		return fmt.Errorf("Failed to encode result to JSON: %v", err)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(writer, string(jsonTrains))
	return nil
}

func buildDelegateURL(request *http.Request) (string, error) {
	schedUrl := new(bytes.Buffer)
	schedParameters, err := buildSchedQuery(request.URL.Query())
	if err != nil {
		return "", err
	}
	err = delegateUrlTemplate.Execute(schedUrl, schedParameters)
	if err != nil {
		return "", err
	}
	return schedUrl.String(), nil
}

func queryDelegateBackend(context appengine.Context, delegateURL string) (string, error) {
	resp, err := urlfetch.Client(context).Get(delegateURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parseDelegateResult(schedResult string) ([]train, error) {
	trains := make([]train, 0, 10)
	matches := schedResultRegexp.FindAllStringSubmatch(schedResult, -1)
	for _, v := range matches {
		t := train{Destination: v[1], Mission: v[2], Stop: v[3]}
		trains = append(trains, t)
	}
	return trains, nil
}

func mkMarshalFunc(pretty bool) func(interface{}) ([]byte, error) {
	if pretty {
		return func(input interface{}) ([]byte, error) {
			return json.MarshalIndent(input, "", "  ")
		}
	} else {
		return json.Marshal
	}
}

func buildSchedQuery(q map[string][]string) (schedQuery, error) {
	mandatoryParameters := [...]string{"line", "direction", "station"}
	for _, p := range mandatoryParameters {
		if q[p] == nil {
			return schedQuery{}, errors.New("Missing mandatory parameter '" + p + "'")
		}
	}
	return schedQuery{Mode: "rer", Line: q["line"][0], Direction: q["direction"][0], Station: q["station"][0]}, nil
}

type schedQuery struct {
	Mode, Line, Direction, Station string
}
