package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const verifiedDownloadLimit = int64(512 * 1024 * 1024)

var ErrArtifactIntegrity = errors.New("downloaded artifact integrity check failed")

// VerifiedHTTPDownloader saves one bounded archive in a private staging
// directory and publishes it there only after its expected digest matches.
type VerifiedHTTPDownloader struct {
	Client *http.Client
}

// VerifiedArchive verifies the digest inside the acquisition attempt so a
// checksum mismatch never falls through to a mirror.
type VerifiedArchive struct {
	Downloader VerifiedHTTPDownloader
	SHA256     string
	Report     func(Route, int64, int64, bool)
}

func (a VerifiedArchive) Attempt(ctx context.Context, request AttemptRequest) (AttemptResult, error) {
	path := filepath.Join(request.StagingPath, request.Identity.Filename)
	err := a.Downloader.Download(ctx, request.Candidate.Location, path, a.SHA256, func(received, total int64, known bool) {
		if a.Report != nil {
			a.Report(request.Candidate.Route, received, total, known)
		}
	})
	if errors.Is(err, ErrArtifactIntegrity) {
		return AttemptResult{}, Failure{Kind: FailureIntegrity, Summary: "artifact integrity verification failed", Cause: err}
	}
	if err != nil {
		failure := classifyMetadataHTTPFailure(err)
		failure.Summary = "artifact download failed"
		return AttemptResult{}, failure
	}
	return AttemptResult{ResolvedIdentity: request.Identity, PayloadPath: path, Integrity: IntegrityEvidence{Algorithm: "sha256", Digest: strings.ToLower(a.SHA256)}}, nil
}

func (d VerifiedHTTPDownloader) Download(ctx context.Context, source, destination, expectedSHA256 string, report func(int64, int64, bool)) error {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("artifact download returned status %d", response.StatusCode)
	}
	if response.ContentLength > verifiedDownloadLimit {
		return errors.New("artifact exceeds the download limit")
	}
	part := destination + ".part"
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	reader := io.Reader(io.LimitReader(response.Body, verifiedDownloadLimit+1))
	writer := io.MultiWriter(file, hash)
	received, copyErr := io.Copy(writer, &progressReader{reader: reader, report: report, total: response.ContentLength, hasTotal: response.ContentLength >= 0})
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(part)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(part)
		return closeErr
	}
	if received > verifiedDownloadLimit {
		_ = os.Remove(part)
		return errors.New("artifact exceeds the download limit")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(strings.TrimSpace(expectedSHA256), actual) {
		_ = os.Remove(part)
		return fmt.Errorf("%w: expected %s", ErrArtifactIntegrity, strings.TrimSpace(expectedSHA256))
	}
	if err := os.Rename(part, destination); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

type progressReader struct {
	reader   io.Reader
	report   func(int64, int64, bool)
	received int64
	total    int64
	hasTotal bool
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.received += int64(n)
	if r.report != nil {
		r.report(r.received, r.total, r.hasTotal)
	}
	return n, err
}
