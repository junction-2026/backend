package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// wikiCache keeps one JSON file per (language, normalised query, search_mode) under dir.
type wikiCache struct {
	dir string
	ttl time.Duration
}

type wikiCacheFile struct {
	StoredAt time.Time  `json:"stored_at"`
	Lookup   wikiLookup `json:"lookup"`
}

func wikiKey(lang, query, mode string) string {
	h := sha256.Sum256([]byte(lang + "\x00" + normalizeQuery(query) + "\x00" + mode))
	return hex.EncodeToString(h[:])
}

// get returns a stored answer younger than ttl; a missing, expired or corrupt file is a miss.
func (c wikiCache) get(key string) (wikiLookup, bool) {
	b, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return wikiLookup{}, false
	}
	var f wikiCacheFile
	if json.Unmarshal(b, &f) != nil || time.Since(f.StoredAt) > c.ttl {
		return wikiLookup{}, false
	}
	switch l := f.Lookup; {
	case l.Status == "found" && (l.Title == nil || l.URL == nil || l.PageID == nil || l.RevisionID == nil || l.RevisedAt == nil || l.Passage == nil || l.PassageSource == nil):
		return wikiLookup{}, false
	case l.Status != "found" && l.Status != "no_page" && l.Status != "ambiguous":
		return wikiLookup{}, false
	}
	return f.Lookup, true
}

// put stores an answer atomically (temp file, then rename). It is best effort: a write failure only costs a later request.
func (c wikiCache) put(key string, l wikiLookup) {
	b, err := json.Marshal(wikiCacheFile{time.Now().UTC(), l})
	if err != nil || os.MkdirAll(c.dir, 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(c.dir, key+".json")) != nil {
		os.Remove(tmp.Name())
	}
}
