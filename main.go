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
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/storage/v1"
)

const (
	maxArchiveSize = 16 << 20
	sessionsPrefix = "sessions"
)

var legacySessionName = regexp.MustCompile(`^[0-9]{8}T[0-9]{9}Z-([a-f0-9]{32})$`)
var crcTable = crc32.MakeTable(crc32.Castagnoli)

type uploader struct {
	bucket     string
	packageIDs map[string]struct{}
	store      *storage.Service
}

func main() {
	bucket := strings.TrimSpace(os.Getenv("SESSION_DATA_BUCKET"))
	if bucket == "" {
		log.Fatal("SESSION_DATA_BUCKET must name the upload bucket")
	}
	packageIDs := make(map[string]struct{})
	for _, entry := range strings.Split(os.Getenv("SESSION_DATA_PACKAGE_IDS"), ",") {
		id := strings.TrimSpace(entry)
		if !androidPackageID.MatchString(id) {
			log.Fatalf("invalid Android package ID %q in SESSION_DATA_PACKAGE_IDS", id)
		}
		packageIDs[id] = struct{}{}
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
	upload := uploader{bucket: bucket, packageIDs: packageIDs, store: store}
	mux.HandleFunc("POST /sessions", upload.uploadLegacyBasketball)
	mux.HandleFunc("POST /v2/sessions", upload.uploadPackage)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, "status", "ok")
	})
	server := &http.Server{
		Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func (u uploader) uploadLegacyBasketball(w http.ResponseWriter, r *http.Request) {
	if !acceptZip(w, r) {
		return
	}
	if r.Header.Get("X-Game-Id") != "basketball" {
		reply(w, http.StatusBadRequest, "error", "unknown game ID")
		return
	}
	session := r.Header.Get("X-Session-Directory")
	match := legacySessionName.FindStringSubmatch(session)
	if match == nil {
		reply(w, http.StatusBadRequest, "error", "invalid session directory")
		return
	}
	data, ok := readUpload(w, r)
	if !ok {
		return
	}
	if err := validateLegacyArchive(data, match[1]); err != nil {
		reply(w, http.StatusBadRequest, "error", err.Error())
		return
	}

	name := path.Join(sessionsPrefix, session, archiveDigest(data)+".zip")
	u.storeArchive(w, r, name, data, nil)
}

func acceptZip(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/zip" {
		reply(w, http.StatusUnsupportedMediaType, "error", "expected application/zip")
		return false
	}
	return true
}

func readUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxArchiveSize))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			reply(w, http.StatusRequestEntityTooLarge, "error", "session archive exceeds 16 MiB")
		} else {
			reply(w, http.StatusBadRequest, "error", "could not read session archive")
		}
		return nil, false
	}
	return data, true
}

func archiveDigest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (u uploader) storeArchive(w http.ResponseWriter, r *http.Request, name string, data []byte, metadata map[string]string) {
	checksum := binary.BigEndian.AppendUint32(nil, crc32.Checksum(data, crcTable))
	object := &storage.Object{
		Name: name, ContentType: "application/zip", CacheControl: "no-store",
		Crc32c: base64.StdEncoding.EncodeToString(checksum), Metadata: metadata,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_, err := u.store.Objects.Insert(u.bucket, object).
		Media(bytes.NewReader(data), googleapi.ChunkSize(0)).
		IfGenerationMatch(0).Fields("name").Context(ctx).Do()
	// A retry of the same bytes targets the same immutable object.
	var apiErr *googleapi.Error
	if err != nil && !(errors.As(err, &apiErr) && apiErr.Code == http.StatusPreconditionFailed) {
		log.Printf("storing session archive %s: %v", name, err)
		reply(w, http.StatusBadGateway, "error", "could not store session data")
		return
	}
	reply(w, http.StatusOK, "uri", "gs://"+u.bucket+"/"+name)
}

func reply(w http.ResponseWriter, status int, key, value string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{key: value})
}
