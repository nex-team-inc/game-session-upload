package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/storage/v1"
)

const maxArchiveSize = 16 << 20

var sessionName = regexp.MustCompile(`^[0-9]{8}T[0-9]{9}Z-([a-f0-9]{32})$`)
var crcTable = crc32.MakeTable(crc32.Castagnoli)

type uploader struct {
	destinations map[string]destination
	store        *storage.Service
}

type destination struct {
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
}

func main() {
	var destinations map[string]destination
	if err := json.Unmarshal([]byte(os.Getenv("SESSION_DATA_DESTINATIONS")), &destinations); err != nil || len(destinations) == 0 {
		log.Fatal("SESSION_DATA_DESTINATIONS must map game IDs to storage destinations")
	}
	for game, d := range destinations {
		if game == "" || d.Bucket == "" || !fs.ValidPath(d.Prefix) || d.Prefix == "." {
			log.Fatalf("invalid session data destination for game %q", game)
		}
	}
	store, err := storage.NewService(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", uploader{destinations, store}.upload)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, "status", "ok")
	})
	server := &http.Server{
		Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func (u uploader) upload(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/zip" {
		reply(w, http.StatusUnsupportedMediaType, "error", "expected application/zip")
		return
	}
	game := r.Header.Get("X-Game-Id")
	dest, ok := u.destinations[game]
	if !ok {
		reply(w, http.StatusBadRequest, "error", "unknown game ID")
		return
	}
	session := r.Header.Get("X-Session-Directory")
	match := sessionName.FindStringSubmatch(session)
	if match == nil {
		reply(w, http.StatusBadRequest, "error", "invalid session directory")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxArchiveSize))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			reply(w, http.StatusRequestEntityTooLarge, "error", "session archive exceeds 16 MiB")
		} else {
			reply(w, http.StatusBadRequest, "error", "could not read session archive")
		}
		return
	}
	if err := validateArchive(data, match[1]); err != nil {
		reply(w, http.StatusBadRequest, "error", err.Error())
		return
	}

	name := path.Join(dest.Prefix, session, fmt.Sprintf("%x.zip", sha256.Sum256(data)))
	checksum := binary.BigEndian.AppendUint32(nil, crc32.Checksum(data, crcTable))
	object := &storage.Object{
		Name: name, ContentType: "application/zip", CacheControl: "no-store",
		Crc32c: base64.StdEncoding.EncodeToString(checksum),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_, err = u.store.Objects.Insert(dest.Bucket, object).
		Media(bytes.NewReader(data), googleapi.ChunkSize(0)).
		IfGenerationMatch(0).Fields("name").Context(ctx).Do()
	// A retry of the same bytes targets the same immutable object.
	var apiErr *googleapi.Error
	if err != nil && !(errors.As(err, &apiErr) && apiErr.Code == http.StatusPreconditionFailed) {
		log.Printf("storing %s session %s: %v", game, session, err)
		reply(w, http.StatusBadGateway, "error", "could not store session data")
		return
	}
	reply(w, http.StatusOK, "uri", "gs://"+dest.Bucket+"/"+name)
}

func reply(w http.ResponseWriter, status int, key, value string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{key: value})
}
