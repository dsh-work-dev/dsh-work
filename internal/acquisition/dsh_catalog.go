package acquisition

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DSHRelease is one exact version from the public @deepseek-ai/dsh package
// view. Tags and publication time are display metadata, never compatibility
// policy.
type DSHRelease struct {
	Version      string
	Tags         []string
	PublishedAt  string
	Route        Route
	FallbackUsed bool
	ObservedAt   string
}

// DSHCatalogClient performs network I/O only when Refresh is explicitly
// called. The ordinary Manager snapshot reads its persisted result.
type DSHCatalogClient struct {
	Client          *http.Client
	OfficialBaseURL string
	MirrorBaseURL   string
	StagingRoot     string
	MetadataLimit   int64
	Now             func() time.Time
}

func (c DSHCatalogClient) Refresh(ctx context.Context) ([]DSHRelease, error) {
	nodeClient := NodeCatalogClient{
		OfficialBaseURL: c.OfficialBaseURL,
		MirrorBaseURL:   c.MirrorBaseURL,
		StagingRoot:     c.StagingRoot,
		MetadataLimit:   c.MetadataLimit,
		Now:             c.Now,
	}
	nodeClient.Client = c.Client
	nodeClient = nodeClient.normalized()
	result, err := nodeClient.fetch(ctx, "refresh-dsh-releases", ArtifactIdentity{
		Kind: ArtifactDSH, Name: "@deepseek-ai/dsh", Filename: "metadata.json",
	}, "@deepseek-ai%2Fdsh")
	if err != nil {
		return nil, err
	}
	data, readErr := os.ReadFile(result.PayloadPath)
	cleanupErr := removeMetadata(result)
	if readErr != nil {
		return nil, Failure{Kind: FailureLocalIO, Summary: "DSH release metadata could not be read", Cause: readErr}
	}
	if cleanupErr != nil {
		return nil, Failure{Kind: FailureLocalIO, Summary: "DSH release staging could not be cleaned", Cause: cleanupErr}
	}
	return parseDSHReleases(data, result.Route, result.FallbackUsed, nodeClient.Now().UTC())
}

func parseDSHReleases(data []byte, route Route, fallback bool, observedAt time.Time) ([]DSHRelease, error) {
	var metadata struct {
		Versions map[string]json.RawMessage `json:"versions"`
		DistTags map[string]string          `json:"dist-tags"`
		Time     map[string]string          `json:"time"`
	}
	if err := decodeJSON(data, &metadata); err != nil {
		return nil, Failure{Kind: FailureSemantic, Summary: "DSH release metadata is invalid", Cause: err}
	}
	if len(metadata.Versions) == 0 {
		return nil, Failure{Kind: FailureNotFound, Summary: "no DSH releases were found"}
	}
	tagsByVersion := make(map[string][]string)
	for tag, version := range metadata.DistTags {
		if _, ok := metadata.Versions[version]; ok && strings.TrimSpace(tag) != "" {
			tagsByVersion[version] = append(tagsByVersion[version], tag)
		}
	}
	versions := make([]string, 0, len(metadata.Versions))
	for version := range metadata.Versions {
		if exactDSHVersionPattern.MatchString(version) {
			versions = append(versions, version)
		}
	}
	if len(versions) == 0 {
		return nil, Failure{Kind: FailureNotFound, Summary: "no exact DSH releases were found"}
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersion(versions[i], versions[j]) > 0 })
	releases := make([]DSHRelease, 0, len(versions))
	for _, version := range versions {
		tags := append([]string(nil), tagsByVersion[version]...)
		sort.Strings(tags)
		releases = append(releases, DSHRelease{Version: version, Tags: tags, PublishedAt: metadata.Time[version], Route: route, FallbackUsed: fallback, ObservedAt: observedAt.Format(time.RFC3339)})
	}
	return releases, nil
}

var exactDSHVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

func compareVersion(left, right string) int {
	leftMain := strings.SplitN(left, "-", 2)[0]
	rightMain := strings.SplitN(right, "-", 2)[0]
	leftParts := strings.Split(leftMain, ".")
	rightParts := strings.Split(rightMain, ".")
	for index := 0; index < 3; index++ {
		leftNumber, _ := strconv.ParseUint(leftParts[index], 10, 64)
		rightNumber, _ := strconv.ParseUint(rightParts[index], 10, 64)
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
	}
	leftPre := strings.Contains(left, "-")
	rightPre := strings.Contains(right, "-")
	if leftPre != rightPre {
		if leftPre {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}
