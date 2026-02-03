package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type uploadSavedFile struct {
	Original string `json:"original"`
	Name     string `json:"name"`
	Bytes    int64  `json:"bytes"`
}

type uploadResponse struct {
	Directory string            `json:"directory"`
	Files     []uploadSavedFile `json:"files"`
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	remoteIP := extractRemoteIP(r)
	uploadAllowed := true
	if strings.TrimSpace(remoteIP) != "" {
		level, matched := MatchUserLevel(s.userLevels, remoteIP)
		if matched {
			uploadAllowed = level == UserLevelInteract
		} else {
			s.warnNoUserLevelMatch(remoteIP)
		}
	}
	if !uploadAllowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	targetDir, err := s.session.CurrentDirectory()
	if err != nil {
		http.Error(w, "Shell directory not available", http.StatusServiceUnavailable)
		return
	}
	if info, statErr := os.Stat(targetDir); statErr != nil || !info.IsDir() {
		http.Error(w, "Shell directory not available", http.StatusServiceUnavailable)
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Invalid multipart upload", http.StatusBadRequest)
		return
	}

	fmt.Fprintf(os.Stderr, "Upload: receiving files from %s into %s\n", safeLogValue(remoteIP), targetDir)

	var saved []uploadSavedFile
	var totalBytes int64
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "Upload failed", http.StatusBadRequest)
			return
		}
		if part == nil {
			continue
		}

		filename := part.FileName()
		if filename == "" {
			_ = part.Close()
			continue
		}

		safeName := sanitizeFilename(filename)
		if safeName == "" {
			safeName = "upload.bin"
		}

		finalName, file, err := createUniqueFile(targetDir, safeName)
		if err != nil {
			_ = part.Close()
			http.Error(w, "Failed to create upload file", http.StatusInternalServerError)
			return
		}

		n, copyErr := io.Copy(file, part)
		closeErr := file.Close()
		_ = part.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(filepath.Join(targetDir, finalName))
			http.Error(w, "Upload failed", http.StatusInternalServerError)
			return
		}

		saved = append(saved, uploadSavedFile{
			Original: filename,
			Name:     finalName,
			Bytes:    n,
		})
		totalBytes += n

		fmt.Fprintf(os.Stderr, "Upload: saved %s (%d bytes)\n", finalName, n)
	}

	if len(saved) == 0 {
		http.Error(w, "No files received", http.StatusBadRequest)
		return
	}

	fmt.Fprintf(os.Stderr, "Upload: complete (%d file(s), %d bytes)\n", len(saved), totalBytes)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(uploadResponse{
		Directory: targetDir,
		Files:     saved,
	})
}

func sanitizeFilename(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\x00", "")
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	trimmed = path.Base(trimmed)
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return ""
	}

	const invalid = "<>:\"/\\|?*"
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		if strings.ContainsRune(invalid, r) {
			return '_'
		}
		return r
	}, trimmed)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return ""
	}
	return cleaned
}

func createUniqueFile(dir string, filename string) (string, *os.File, error) {
	base, ext := splitName(filename)
	for counter := 0; counter < 10000; counter++ {
		candidate := filename
		if counter > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", base, counter, ext)
		}
		fullPath := filepath.Join(dir, candidate)
		file, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return candidate, file, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return "", nil, err
	}
	return "", nil, errors.New("too many name collisions")
}

func splitName(filename string) (base string, ext string) {
	ext = filepath.Ext(filename)
	base = strings.TrimSuffix(filename, ext)
	if base == "" {
		base = filename
		ext = ""
	}
	return base, ext
}

func safeLogValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}
