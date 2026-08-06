package utils

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"tape/common/logger"
	"time"
)

// httpTimeout bounds a whole transfer. There was previously no timeout
// anywhere in the codebase, so a hung mirror pinned a daemon goroutine forever.
const httpTimeout = 30 * time.Minute

// DownloadFile fetches url into filepath and returns the number of bytes
// actually written.
//
// The write is atomic: content lands in a sibling temporary file and is renamed
// into place only on success. The previous implementation truncated the
// destination with os.Create *before* issuing the request, so any failure --
// bad status, a connection dropped mid-copy -- left a zero-length or partial
// file behind. For the repo index that silently turned every subsequent query
// into "package not found".
func DownloadFile(url string, filepath string, skipTls bool, progressReport func(progress int8)) (int64, error) {
	log := logger.NewLogger("common", "utils.DownloadFile")

	if err := os.MkdirAll(path.Dir(filepath), 0755); err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}

	// Same directory as the destination, so the rename stays on one filesystem.
	tmp, err := os.CreateTemp(path.Dir(filepath), "."+path.Base(filepath)+".part-")
	if err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}
	tmpName := tmp.Name()

	// Removed unless the rename below succeeds.
	committed := false
	defer func() {
		tmp.Close()
		if !committed {
			os.Remove(tmpName)
		}
	}()

	var written int64

	log.VerboseInfo("Downloading file: " + url)
	if strings.HasPrefix(url, "/") {
		written, err = copyLocalFile(url, tmp, progressReport)
	} else {
		written, err = copyRemoteFile(url, tmp, skipTls, progressReport)
	}
	if err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}

	if err := tmp.Sync(); err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}
	// CreateTemp makes the file 0600; give it a predictable mode before it
	// becomes the visible artifact.
	if err := os.Chmod(tmpName, 0644); err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}
	if err := os.Rename(tmpName, filepath); err != nil {
		log.VerboseError(err.Error())
		return 0, err
	}
	committed = true

	return written, nil
}

func copyRemoteFile(url string, dst io.Writer, skipTls bool, progressReport func(int8)) (int64, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTls},
	}
	client := &http.Client{Transport: tr, Timeout: httpTimeout}

	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bad status: %s", resp.Status)
	}

	counter := &progressWriter{
		Length:         resp.ContentLength,
		ProgressReport: progressReport,
	}

	written, err := io.Copy(io.MultiWriter(dst, counter), resp.Body)
	if err != nil {
		return written, err
	}

	// Trust the bytes actually written, not the server's claim. Returning
	// resp.ContentLength meant a truncated transfer reported success, and a
	// chunked response (-1) propagated a negative "size" to the caller.
	if resp.ContentLength > 0 && written != resp.ContentLength {
		return written, fmt.Errorf("incomplete download: got %d bytes, expected %d", written, resp.ContentLength)
	}

	if progressReport != nil {
		progressReport(100)
	}

	return written, nil
}

func copyLocalFile(src string, dst io.Writer, progressReport func(int8)) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer srcFile.Close()

	written, err := io.Copy(dst, srcFile)
	if err != nil {
		return written, err
	}

	if progressReport != nil {
		progressReport(100)
	}

	return written, nil
}

// progressWriter reports download progress as a percentage.
type progressWriter struct {
	Length         int64
	Current        int64
	LastProgress   int8
	ProgressReport func(int8)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.Current += int64(n)

	// Length is -1 for chunked and transparently-decompressed responses, and 0
	// for an empty body. Dividing by it crashed the daemon outright; there is
	// simply no percentage to report when the total is unknown.
	if pw.Length <= 0 || pw.ProgressReport == nil {
		return n, nil
	}

	progress := int8((pw.Current * 100) / pw.Length)
	if progress > 100 {
		progress = 100
	}
	if progress != pw.LastProgress {
		pw.ProgressReport(progress)
		pw.LastProgress = progress
	}

	return n, nil
}
