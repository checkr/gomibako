package main

import (
	"errors"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Songmu/strrand"
	"github.com/dimfeld/httptreemux"
	"github.com/urfave/negroni"
)

func _templates() map[string]*template.Template {
	templates := make(map[string]*template.Template)
	names := []string{"index", "inspect"}

	for _, name := range names {
		templates[name] =
			template.Must(template.ParseFiles(
				"templates/layout.tmpl.html", "templates/"+name+".tmpl.html"))
	}

	return templates
}

type gomibakoKey string

type gomibakoRequest struct {
	timestamp     time.Time
	method        string
	url           *url.URL
	headers       http.Header
	body          []byte
	contentLength int64
}

type gomibako struct {
	key      gomibakoKey
	requests []*gomibakoRequest
}

type gomibakoRepository struct {
	gomibakos map[gomibakoKey]*gomibako
	mutex     sync.RWMutex
}

func newGomibakoRepository() *gomibakoRepository {
	gr := gomibakoRepository{
		gomibakos: make(map[gomibakoKey]*gomibako),
		mutex:     sync.RWMutex{},
	}
	return &gr
}

func (gr *gomibakoRepository) AddGomibako() (*gomibako, error) {
	gr.mutex.Lock()
	defer gr.mutex.Unlock()

	str, err := strrand.RandomString("[a-z0-9]{10}")
	if err != nil {
		return nil, err
	}
	newKey := gomibakoKey(str)

	gr.gomibakos[newKey] = &gomibako{
		key:      newKey,
		requests: make([]*gomibakoRequest, 0),
	}

	return gr.gomibakos[newKey], nil
}

func (gr *gomibakoRepository) AddRequest(key gomibakoKey, greq *gomibakoRequest) error {
	gr.mutex.Lock()
	defer gr.mutex.Unlock()

	g, ok := gr.gomibakos[key]
	if !ok {
		return errors.New("no gomibako found")
	}
	g.requests = append(g.requests, greq)
	return nil
}

func (gr *gomibakoRepository) Get(key gomibakoKey) (*gomibako, error) {
	gr.mutex.RLock()
	defer gr.mutex.RUnlock()

	g, ok := gr.gomibakos[key]
	if !ok {
		return nil, errors.New("no gomibako found")
	}
	return g, nil
}

type ViewableHeaderPair struct {
	Key   string
	Value string
}

type ViewableHeaders []*ViewableHeaderPair

func (hs ViewableHeaders) Len() int      { return len(hs) }
func (hs ViewableHeaders) Swap(i, j int) { hs[i], hs[j] = hs[j], hs[i] }
func (hs ViewableHeaders) Less(i, j int) bool {
	if hs[i].Key == hs[j].Key {
		return hs[i].Value < hs[j].Value
	} else {
		return hs[i].Key < hs[j].Key
	}
}

type ViewableGomibakoRequest struct {
	Timestamp     string
	Method        string
	URL           string
	Headers       ViewableHeaders
	Body          string
	ContentLength string
}

func newViewableGomibakoRequest(greq *gomibakoRequest) *ViewableGomibakoRequest {
	var viewableHeaders ViewableHeaders
	for k, vs := range greq.headers {
		for _, v := range vs {
			viewableHeaders = append(viewableHeaders, &ViewableHeaderPair{k, v})
		}
	}
	sort.Sort(viewableHeaders)
	return &ViewableGomibakoRequest{
		Timestamp:     greq.timestamp.String(),
		Method:        greq.method,
		URL:           greq.url.String(),
		Headers:       viewableHeaders,
		Body:          string(greq.body),
		ContentLength: strconv.FormatInt(greq.contentLength, 10),
	}
}

func main() {

	gr := newGomibakoRepository()
	router := httptreemux.New()
	templates := _templates()

	group := router.UsingContext()
	group.GET("/", func(w http.ResponseWriter, r *http.Request) {
		type Inventry struct {
			Title string
		}
		err := templates["index"].Execute(w, Inventry{Title: "My index page"})
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		}
	})
	group.POST("/g/-/new", func(w http.ResponseWriter, r *http.Request) {
		g, err := gr.AddGomibako()
		if err != nil {
			http.Error(w, "gomibako generation error: "+err.Error(), http.StatusInternalServerError)
		}
		http.Redirect(w, r, "/g/"+string(g.key)+"/inspect", 302)
	})
	group.GET("/g/:gomibakokey/inspect", func(w http.ResponseWriter, r *http.Request) {
		params := httptreemux.ContextParams(r.Context())
		gomibakoKey := gomibakoKey(params["gomibakokey"])
		g, err := gr.Get(gomibakoKey)
		if err != nil {
			http.Error(w, "no gomibako found", http.StatusNotFound)
			return
		}

		requests := make([]*ViewableGomibakoRequest, len(g.requests))
		for i, r := range g.requests {
			requests[(len(g.requests)-1)-i] = newViewableGomibakoRequest(r)
		}

		type Inventry struct {
			Title    string
			Requests []*ViewableGomibakoRequest
		}

		inv := Inventry{Title: "gomibako: " + string(gomibakoKey), Requests: requests}
		err = templates["inspect"].Execute(w, inv)
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
	})
	recordReq := func(w http.ResponseWriter, r *http.Request) {
		params := httptreemux.ContextParams(r.Context())
		gomibakoKey := gomibakoKey(params["gomibakokey"])

		reader := http.MaxBytesReader(w, r.Body, 3*1000*1000)
		defer reader.Close()
		bodyBytes, err := ioutil.ReadAll(reader)
		if err != nil {
			http.Error(w, "failed to load body", http.StatusBadRequest)
			return
		}

		greq := &gomibakoRequest{
			timestamp: time.Now(),
			method:    r.Method,
			url:       r.URL,
			headers:   r.Header,
			body:      bodyBytes,
		}
		gr.AddRequest(gomibakoKey, greq)

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok\n")
	}
	group.GET("/g/:gomibakokey", recordReq)
	group.POST("/g/:gomibakokey", recordReq)

	n := negroni.Classic()
	n.UseHandler(router)

	log.Fatal(http.ListenAndServe(":8000", n))
}
