package progress

import (
	"encoding/json"
	"io"
	"sync"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

type Event struct {
	Type    string `json:"type"`
	Phase   string `json:"phase,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Message string `json:"message,omitempty"`
	Current int64  `json:"current,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Status  string `json:"status,omitempty"`
}

type JSONEmitter struct {
	enc *json.Encoder
	mu  sync.Mutex
}

func NewJSONEmitter(w io.Writer) *JSONEmitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &JSONEmitter{enc: enc}
}

func (e *JSONEmitter) Emit(event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(event)
}

func (e *JSONEmitter) Phase(phase, ref string) {
	e.Emit(Event{Type: "phase", Phase: phase, Ref: ref})
}

func (e *JSONEmitter) Status(message string) {
	e.Emit(Event{Type: "status", Message: message})
}

func (e *JSONEmitter) Done(status, message string) {
	e.Emit(Event{Type: "done", Status: status, Message: message})
}

func (e *JSONEmitter) Error(message string) {
	e.Emit(Event{Type: "error", Message: message})
}

func (e *JSONEmitter) Channel(stage string) chan<- v1.Update {
	ch := make(chan v1.Update, 128)
	go func() {
		for upd := range ch {
			if upd.Error != nil {
				if upd.Error == io.EOF {
					continue
				}
				e.Error(upd.Error.Error())
				continue
			}
			e.Emit(Event{
				Type:    "progress",
				Stage:   stage,
				Current: upd.Complete,
				Total:   upd.Total,
			})
		}
	}()
	return ch
}
