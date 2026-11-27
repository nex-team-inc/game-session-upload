package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

const maxZIPIndexSize = 2 << 20

type legacyManifest struct {
	Version int               `json:"schema_version"`
	ID      string            `json:"session_id"`
	Status  string            `json:"status"`
	Files   map[string]string `json:"files"`
	Photo   struct {
		Status string `json:"status"`
		File   string `json:"file"`
	} `json:"photo"`
}

var legacySessionFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Bound the ZIP index scan before archive/zip has allocated every entry.
type archiveReader struct {
	*bytes.Reader
	indexBudget int
}

func (r *archiveReader) ReadAt(p []byte, off int64) (int, error) {
	if r.indexBudget >= 0 {
		if len(p) > r.indexBudget {
			return 0, errors.New("oversized ZIP index")
		}
		r.indexBudget -= len(p)
	}
	return r.Reader.ReadAt(p, off)
}

func validateOpaqueZIP(data []byte) error {
	source := &archiveReader{Reader: bytes.NewReader(data), indexBudget: maxZIPIndexSize}
	z, err := zip.NewReader(source, int64(len(data)))
	if err != nil {
		return errors.New("expected a valid ZIP archive")
	}

	remaining := uint64(maxArchiveSize)
	for _, file := range z.File {
		if file.UncompressedSize64 > remaining {
			return errors.New("uncompressed ZIP exceeds 16 MiB")
		}
		remaining -= file.UncompressedSize64
	}
	return nil
}

func validateLegacyArchive(data []byte, sessionID string) error {
	source := &archiveReader{Reader: bytes.NewReader(data), indexBudget: maxZIPIndexSize}
	z, err := zip.NewReader(source, int64(len(data)))
	if err != nil || len(z.File) < 2 || len(z.File) > 32 {
		return errors.New("expected a session ZIP with 2 to 32 files")
	}
	source.indexBudget = -1
	files := make(map[string]*zip.File, len(z.File))
	remaining := uint64(maxArchiveSize)
	for _, file := range z.File {
		if !legacySessionFilename.MatchString(file.Name) || files[file.Name] != nil || !file.Mode().IsRegular() || file.UncompressedSize64 > remaining {
			return errors.New("invalid session ZIP entry or uncompressed size")
		}
		remaining -= file.UncompressedSize64
		files[file.Name] = file
	}

	entry := files["session.json"]
	if entry == nil || entry.UncompressedSize64 > 256<<10 {
		return errors.New("missing or oversized session manifest")
	}
	var contents bytes.Buffer
	if err := readEntry(entry, &contents); err != nil {
		return err
	}
	var m legacyManifest
	if err := json.Unmarshal(contents.Bytes(), &m); err != nil || m.Version < 1 || m.ID != sessionID {
		return errors.New("manifest does not match the session")
	}
	switch m.Status {
	case "completed", "cancelled", "restarted", "application_quit", "disabled", "destroyed", "recording_error":
	default:
		return errors.New("session must be finished")
	}

	if len(m.Files) == 0 {
		return errors.New("invalid session file manifest")
	}
	delete(files, entry.Name)
	for _, name := range m.Files {
		if files[name] == nil {
			return errors.New("missing or unexpected session file")
		}
		delete(files, name)
	}
	if m.Photo.Status == "captured" {
		if files[m.Photo.File] == nil {
			return errors.New("invalid tester photo")
		}
		delete(files, m.Photo.File)
	}
	if len(files) != 0 {
		return errors.New("unexpected file in session ZIP")
	}

	for _, file := range z.File {
		if file == entry {
			continue
		}
		if err := readEntry(file, io.Discard); err != nil {
			return err
		}
	}
	return nil
}

func readEntry(file *zip.File, dst io.Writer) error {
	r, err := file.Open()
	if err != nil {
		return errors.New("unreadable ZIP entry")
	}
	defer r.Close()
	// Reading through EOF verifies the ZIP checksum. Limit work to the declared size.
	size := int64(file.UncompressedSize64)
	n, err := io.Copy(dst, io.LimitReader(r, size+1))
	if err != nil || n != size {
		return errors.New("damaged ZIP entry")
	}
	return nil
}
