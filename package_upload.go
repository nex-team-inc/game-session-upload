package main

import (
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

var androidPackageID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
var rawSessionID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (u uploader) uploadPackage(w http.ResponseWriter, r *http.Request) {
	if !acceptZip(w, r) {
		return
	}
	packageID := r.Header.Get("X-Package-Id")
	dest, ok := u.destinations[packageID]
	if !ok || !androidPackageID.MatchString(packageID) {
		reply(w, http.StatusBadRequest, "error", "unknown Android package ID")
		return
	}
	sessionID := r.Header.Get("X-Session-Id")
	if !rawSessionID.MatchString(sessionID) {
		reply(w, http.StatusBadRequest, "error", "invalid session ID")
		return
	}
	started, err := time.Parse(time.RFC3339Nano, r.Header.Get("X-Session-Started-At"))
	if err != nil || started.IsZero() {
		reply(w, http.StatusBadRequest, "error", "invalid session start timestamp")
		return
	}
	version := r.Header.Get("X-App-Version")
	if len(version) > 128 {
		reply(w, http.StatusBadRequest, "error", "app version is too long")
		return
	}
	data, ok := readUpload(w, r)
	if !ok {
		return
	}
	if err := validateOpaqueZIP(data); err != nil {
		reply(w, http.StatusBadRequest, "error", err.Error())
		return
	}

	started = started.UTC()
	timestamp := strings.Replace(started.Format("20060102T150405.000Z"), ".", "", 1)
	name := path.Join(dest.Prefix, packageID, timestamp+"-"+sessionID+"-"+archiveDigest(data)+".zip")
	metadata := map[string]string{
		"package_id":     packageID,
		"session_id":     sessionID,
		"started_at_utc": started.Format(time.RFC3339Nano),
	}
	if version != "" {
		metadata["app_version"] = version
	}
	u.storeArchive(w, r, dest, name, data, metadata)
}
