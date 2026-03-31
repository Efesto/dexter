package lsp

import "sync"

// DocumentStore tracks the text content of open buffers.
type DocumentStore struct {
	mu   sync.RWMutex
	docs map[string]string // URI -> full text content
}

func NewDocumentStore() *DocumentStore {
	return &DocumentStore{docs: make(map[string]string)}
}

func (ds *DocumentStore) Open(uri string, text string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.docs[uri] = text
}

func (ds *DocumentStore) Update(uri string, text string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.docs[uri] = text
}

func (ds *DocumentStore) Close(uri string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	delete(ds.docs, uri)
}

func (ds *DocumentStore) Get(uri string) (string, bool) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	text, ok := ds.docs[uri]
	return text, ok
}
