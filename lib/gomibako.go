package gomibako

import (
	"container/list"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Songmu/strrand"
)

const ttlInHours = 7 * 24

type GomibakoKey string

type GomibakoRequest struct {
	Key           GomibakoKey
	Timestamp     time.Time
	Method        string
	URL           *url.URL
	Headers       http.Header
	Body          []byte
	ContentLength int
}

type Gomibako struct {
	Key       GomibakoKey
	timestamp time.Time
	reqs      *list.List
	chs       map[chan *GomibakoRequest]bool
	mutex     sync.RWMutex
}

func (g *Gomibako) addCh(ch chan *GomibakoRequest) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	g.chs[ch] = true
}

func (g *Gomibako) releaseCh(ch chan *GomibakoRequest) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	delete(g.chs, ch)
}

func (g *Gomibako) releaseChAll() {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	for ch := range g.chs {
		close(ch)
	}
	g.chs = make(map[chan *GomibakoRequest]bool)
}

func (g *Gomibako) addRequest(greq *GomibakoRequest) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	g.reqs.PushBack(greq)
	if g.reqs.Len() > 10 {
		g.reqs.Remove(g.reqs.Front())
	}
}

func (g *Gomibako) Requests() []*GomibakoRequest {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	var reqs []*GomibakoRequest
	for r := g.reqs.Front(); r != nil; r = r.Next() {
		reqs = append(reqs, r.Value.(*GomibakoRequest))
	}
	return reqs
}

func (g *Gomibako) channels() []chan *GomibakoRequest {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	var chs []chan *GomibakoRequest
	for ch, _ := range g.chs {
		chs = append(chs, ch)
	}
	return chs
}

type GomibakoRepository struct {
	gomibakos map[GomibakoKey]*Gomibako
	broker    chan *GomibakoRequest
	mutex     sync.RWMutex
}

func NewGomibakoRepository() *GomibakoRepository {
	gr := GomibakoRepository{
		gomibakos: make(map[GomibakoKey]*Gomibako),
		broker:    make(chan *GomibakoRequest, 100),
		mutex:     sync.RWMutex{},
	}
	return &gr
}

func (gr *GomibakoRepository) AddGomibako() (*Gomibako, error) {
	gr.mutex.Lock()
	defer gr.mutex.Unlock()

	str, err := strrand.RandomString("[a-z0-9]{10}")
	if err != nil {
		return nil, err
	}
	newKey := GomibakoKey(str)

	gr.gomibakos[newKey] = &Gomibako{
		Key:       newKey,
		timestamp: time.Now(),
		reqs:      list.New(),
		chs:       make(map[chan *GomibakoRequest]bool),
	}

	return gr.gomibakos[newKey], nil
}

func (gr *GomibakoRepository) AddRequest(greq *GomibakoRequest) error {
	gr.mutex.Lock()
	defer gr.mutex.Unlock()

	g, ok := gr.gomibakos[greq.Key]
	if !ok {
		return errors.New("no gomibako found")
	}

	g.addRequest(greq)
	gr.broker <- greq

	return nil
}

func (gr *GomibakoRepository) Get(key GomibakoKey) (*Gomibako, error) {
	gr.mutex.RLock()
	defer gr.mutex.RUnlock()

	g, ok := gr.gomibakos[key]
	if !ok {
		return nil, errors.New("no gomibako found")
	}

	return g, nil
}

func (gr *GomibakoRepository) GetWithCh(key GomibakoKey) (*Gomibako, chan *GomibakoRequest, error) {
	g, err := gr.Get(key)
	if err != nil {
		return nil, nil, err
	}

	ch := make(chan *GomibakoRequest)
	g.addCh(ch)

	return g, ch, nil
}

func (gr *GomibakoRepository) Release(key GomibakoKey, ch chan *GomibakoRequest) error {
	gr.mutex.RLock()
	defer gr.mutex.RUnlock()

	g, ok := gr.gomibakos[key]
	if !ok {
		return errors.New("no gomibako found")
	}

	g.releaseCh(ch)
	close(ch)

	return nil
}

func (gr *GomibakoRepository) Truncate() {
	gr.mutex.Lock()
	defer gr.mutex.Unlock()

	log.Println("Start truncating")
	var ids []string
	for gk, g := range gr.gomibakos {
		d := time.Since(g.timestamp)

		if d.Hours() > ttlInHours {
			log.Println("Deleting " + string(gk))
			g.releaseChAll()
			delete(gr.gomibakos, gk)
		} else {
			ids = append(ids, string(gk))
		}
	}
	log.Println("Current gomibakos:", strings.Join(ids, ", "))
}

func (gr *GomibakoRepository) RunBroker() {
	for newGreq := range gr.broker {
		gr.mutex.RLock()
		g, ok := gr.gomibakos[newGreq.Key]
		if !ok {
			continue
		}
		for _, ch := range g.channels() {
			ch <- newGreq
		}
		gr.mutex.RUnlock()
	}
}

func (gr *GomibakoRepository) RunTruncater() {
	t := time.Tick(5 * time.Minute)

	for _ = range t {
		gr.Truncate()
	}
}
