package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const defaultMetadataLimit = int64(8 * 1024 * 1024)

type NodeRelease struct {
	Version      string
	Platform     string
	Architecture string
	Filename     string
	SHA256       string
	Route        Route
	FallbackUsed bool
	ObservedAt   string
}

// NodeCatalogClient resolves Node's official latest stable exact release and
// archive checksum. It performs network I/O only when RefreshLatest is called.
type NodeCatalogClient struct {
	Client          *http.Client
	OfficialBaseURL string
	MirrorBaseURL   string
	Platform        string
	Architecture    string
	StagingRoot     string
	MetadataLimit   int64
	Now             func() time.Time
}

func (c NodeCatalogClient) RefreshLatest(ctx context.Context) (NodeRelease, error) {
	c = c.normalized()
	archiveTag, archiveSuffix, err := nodeArchiveTarget(c.Platform, c.Architecture)
	if err != nil {
		return NodeRelease{}, err
	}
	indexResult, err := c.fetch(ctx, "node-index", ArtifactIdentity{Kind: ArtifactNode, Name: "node", Filename: "index.json"}, "index.json")
	if err != nil {
		return NodeRelease{}, err
	}
	indexData, readErr := os.ReadFile(indexResult.PayloadPath)
	cleanupErr := removeMetadata(indexResult)
	if readErr != nil {
		return NodeRelease{}, Failure{Kind: FailureLocalIO, Summary: "Node release metadata could not be read", Cause: readErr}
	}
	if cleanupErr != nil {
		return NodeRelease{}, Failure{Kind: FailureLocalIO, Summary: "Node release staging could not be cleaned", Cause: cleanupErr}
	}
	version, err := selectLatestStableNode(indexData, archiveTag)
	if err != nil {
		return NodeRelease{}, err
	}
	filename := "node-" + version + "-" + archiveSuffix + ".zip"
	checksumPath := version + "/SHASUMS256.txt"
	checksumResult, err := c.fetch(ctx, "node-checksums-"+version, ArtifactIdentity{
		Kind: ArtifactNode, Name: "node", Version: version, Platform: c.Platform,
		Architecture: c.Architecture, Filename: "SHASUMS256.txt",
	}, checksumPath)
	if err != nil {
		return NodeRelease{}, err
	}
	checksumData, readErr := os.ReadFile(checksumResult.PayloadPath)
	cleanupErr = removeMetadata(checksumResult)
	if readErr != nil {
		return NodeRelease{}, Failure{Kind: FailureLocalIO, Summary: "Node checksum metadata could not be read", Cause: readErr}
	}
	if cleanupErr != nil {
		return NodeRelease{}, Failure{Kind: FailureLocalIO, Summary: "Node checksum staging could not be cleaned", Cause: cleanupErr}
	}
	digest, err := checksumForFile(checksumData, filename)
	if err != nil {
		return NodeRelease{}, err
	}
	route := RouteOfficial
	fallback := indexResult.FallbackUsed || checksumResult.FallbackUsed
	if fallback {
		route = RouteMirror
	}
	return NodeRelease{
		Version: version, Platform: c.Platform, Architecture: c.Architecture,
		Filename: filename, SHA256: digest, Route: route, FallbackUsed: fallback,
		ObservedAt: c.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (c NodeCatalogClient) normalized() NodeCatalogClient {
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 30 * time.Second}
	}
	c.OfficialBaseURL = strings.TrimRight(strings.TrimSpace(c.OfficialBaseURL), "/") + "/"
	c.MirrorBaseURL = strings.TrimRight(strings.TrimSpace(c.MirrorBaseURL), "/")
	if c.MirrorBaseURL != "" {
		c.MirrorBaseURL += "/"
	}
	if c.StagingRoot == "" {
		c.StagingRoot = os.TempDir()
	}
	if c.MetadataLimit <= 0 {
		c.MetadataLimit = defaultMetadataLimit
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

func (c NodeCatalogClient) fetch(ctx context.Context, operation string, identity ArtifactIdentity, relativePath string) (Result, error) {
	if strings.TrimSpace(c.OfficialBaseURL) == "/" {
		return Result{}, Failure{Kind: FailureSemantic, Summary: "official Node release source is unavailable"}
	}
	if err := os.MkdirAll(c.StagingRoot, 0o700); err != nil {
		return Result{}, Failure{Kind: FailureLocalIO, Summary: "Node release staging is unavailable", Cause: err}
	}
	candidates := []SourceCandidate{{Route: RouteOfficial, Location: c.OfficialBaseURL + relativePath}}
	if c.MirrorBaseURL != "" {
		candidates = append(candidates, SourceCandidate{Route: RouteMirror, Location: c.MirrorBaseURL + relativePath})
	}
	return Acquire(ctx, Request{
		OperationID: operation, Identity: identity, Candidates: candidates,
		StagingRoot: c.StagingRoot, StagePrefix: ".node-metadata-",
	}, httpMetadataAdapter{client: c.Client, limit: c.MetadataLimit}, nil)
}

type httpMetadataAdapter struct {
	client *http.Client
	limit  int64
}

func (a httpMetadataAdapter) Attempt(ctx context.Context, request AttemptRequest) (AttemptResult, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, request.Candidate.Location, nil)
	if err != nil {
		return AttemptResult{}, Failure{Kind: FailureSemantic, Summary: "release metadata request is invalid", Cause: err}
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return AttemptResult{}, classifyMetadataHTTPFailure(err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return AttemptResult{}, classifyMetadataHTTPFailure(fmt.Errorf("metadata request returned status %d", response.StatusCode))
	}
	if response.ContentLength > a.limit {
		return AttemptResult{}, Failure{Kind: FailureSemantic, Summary: "release metadata exceeds the size limit"}
	}
	payload := filepath.Join(request.StagingPath, request.Identity.Filename)
	file, err := os.OpenFile(payload, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return AttemptResult{}, Failure{Kind: FailureLocalIO, Summary: "release metadata staging failed", Cause: err}
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, a.limit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return AttemptResult{}, classifyMetadataHTTPFailure(errors.Join(copyErr, closeErr))
	}
	if written > a.limit {
		return AttemptResult{}, Failure{Kind: FailureSemantic, Summary: "release metadata exceeds the size limit"}
	}
	return AttemptResult{ResolvedIdentity: request.Identity, PayloadPath: payload}, nil
}

func classifyMetadataHTTPFailure(err error) Failure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Failure{Kind: FailureCanceled, Summary: "release metadata refresh was canceled", Cause: err}
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "status 401") || strings.Contains(text, "certificate") {
		return Failure{Kind: FailureAuthentication, Summary: "release metadata authentication failed", Cause: err}
	}
	if strings.Contains(text, "status 403") {
		return Failure{Kind: FailureAuthorization, Summary: "release metadata authorization failed", Cause: err}
	}
	if strings.Contains(text, "status 404") {
		return Failure{Kind: FailureNotFound, Summary: "release metadata was not found", Cause: err}
	}
	if strings.Contains(text, "status 500") || strings.Contains(text, "status 502") || strings.Contains(text, "status 503") || strings.Contains(text, "status 504") {
		return Failure{Kind: FailureReachability, Summary: "release metadata source could not be reached", Cause: err}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return Failure{Kind: FailureReachability, Summary: "release metadata source could not be reached", Cause: err}
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return Failure{Kind: FailureLocalIO, Summary: "release metadata staging failed", Cause: err}
	}
	return Failure{Kind: FailureSemantic, Summary: "release metadata refresh failed", Cause: err}
}

var exactNodeVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func selectLatestStableNode(data []byte, archiveTag string) (string, error) {
	var entries []struct {
		Version string   `json:"version"`
		Files   []string `json:"files"`
	}
	if err := decodeJSON(data, &entries); err != nil {
		return "", Failure{Kind: FailureSemantic, Summary: "Node release metadata is invalid", Cause: err}
	}
	for _, entry := range entries {
		if !exactNodeVersionPattern.MatchString(entry.Version) {
			continue
		}
		for _, file := range entry.Files {
			if file == archiveTag {
				return entry.Version, nil
			}
		}
	}
	return "", Failure{Kind: FailureNotFound, Summary: "no stable Node release has an archive for this platform"}
}

func decodeJSON(data []byte, target any) error {
	return json.Unmarshal(data, target)
}

func checksumForFile(data []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != filename {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != 64 {
			break
		}
		for _, character := range digest {
			if !strings.ContainsRune("0123456789abcdef", character) {
				return "", Failure{Kind: FailureIntegrity, Summary: "Node checksum metadata is invalid"}
			}
		}
		return digest, nil
	}
	return "", Failure{Kind: FailureIntegrity, Summary: "Node archive checksum is missing"}
}

func nodeArchiveTarget(platform, architecture string) (string, string, error) {
	if platform != "windows" {
		return "", "", Failure{Kind: FailureNotFound, Summary: "Node archive is unavailable for this platform"}
	}
	switch architecture {
	case "amd64", "x64":
		return "win-x64-zip", "win-x64", nil
	case "arm64":
		return "win-arm64-zip", "win-arm64", nil
	default:
		return "", "", Failure{Kind: FailureNotFound, Summary: "Node archive is unavailable for this architecture"}
	}
}

// A metadata attempt owns one file, not a directory tree. Remove exactly that
// file and its empty staging directory; unexpected entries remain untouched.
// This also avoids recursive Windows directory traversal for a single payload.
func removeMetadata(result Result) error {
	return errors.Join(os.Remove(result.PayloadPath), os.Remove(result.StagingPath))
}
