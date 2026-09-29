/*
 *     Copyright 2025 The ModelPack Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	modelspec "github.com/modelpack/model-spec/specs-go/v1"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelpack/modctl/pkg/config"
)

// digestRegistry serves one model artifact the way a registry does: its
// manifest under the tag "latest" and under its own digest, and its layer.
// It records every manifest reference it was asked for.
type digestRegistry struct {
	server         *httptest.Server
	manifest       []byte
	manifestDigest godigest.Digest
	asked          []string
}

const digestRefFile = "weights.bin contents"

func newDigestRegistry(t *testing.T) *digestRegistry {
	t.Helper()
	layerDigest := godigest.FromString(digestRefFile)
	m := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ocispec.Descriptor{MediaType: modelspec.MediaTypeModelConfig, Digest: godigest.FromString("{}"), Size: 2},
		Layers: []ocispec.Descriptor{{
			MediaType:   modelspec.MediaTypeModelWeightRaw,
			Digest:      layerDigest,
			Size:        int64(len(digestRefFile)),
			Annotations: map[string]string{modelspec.AnnotationFilepath: "weights.bin"},
		}},
	}
	m.SchemaVersion = 2
	body, err := json.Marshal(m)
	require.NoError(t, err)

	r := &digestRegistry{manifest: body, manifestDigest: godigest.FromBytes(body)}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		const prefix = "/v2/test/model/manifests/"
		switch {
		case req.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(req.URL.Path, prefix):
			ref := strings.TrimPrefix(req.URL.Path, prefix)
			r.asked = append(r.asked, ref)
			if ref != "latest" && ref != r.manifestDigest.String() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", r.manifestDigest.String())
			w.Header().Set("Content-Length", fmt.Sprint(len(r.manifest)))
			if req.Method != http.MethodHead {
				_, _ = w.Write(r.manifest)
			}
		case req.URL.Path == fmt.Sprintf("/v2/test/model/blobs/%s", layerDigest):
			_, _ = w.Write([]byte(digestRefFile))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *digestRegistry) repo() string {
	return strings.TrimPrefix(r.server.URL, "http://") + "/test/model"
}

// A wrong digest: well-formed, and not this manifest's.
var wrongDigest = godigest.FromString("some other manifest")

func fetchCfg(t *testing.T) *config.Fetch {
	return &config.Fetch{Output: t.TempDir(), Patterns: []string{"*"}, PlainHTTP: true, Concurrency: 1}
}

// A reference that is only a digest was refused ("invalid reference")
// because the manifest was always fetched by ref.Tag(), which is empty.
func TestFetchByDigestOnly(t *testing.T) {
	r := newDigestRegistry(t)
	cfg := fetchCfg(t)

	err := (&backend{}).Fetch(context.Background(), r.repo()+"@"+r.manifestDigest.String(), cfg)

	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(cfg.Output, "weights.bin"))
	require.NoError(t, err)
	assert.Equal(t, digestRefFile, string(got))
	assert.Equal(t, []string{r.manifestDigest.String()}, r.asked, "the registry is asked for the digest, not a tag")
}

// `repo:tag@digest` fetched the tag and ignored the digest, so a pin to
// the wrong content succeeded: the digest a caller wrote was never checked.
func TestFetchTagWithWrongDigestIsRefused(t *testing.T) {
	r := newDigestRegistry(t)

	err := (&backend{}).Fetch(context.Background(), r.repo()+":latest@"+wrongDigest.String(), fetchCfg(t))

	require.Error(t, err, "a digest that is not the artifact's must fail the fetch")
	assert.NotContains(t, r.asked, "latest", "the digest decides what is fetched, not the tag beside it")
}

// The same two faults in pull, on the path an init container uses:
// extract straight from the registry, no local store.
func TestPullExtractFromRemoteByDigestOnly(t *testing.T) {
	r := newDigestRegistry(t)
	out := t.TempDir()
	cfg := &config.Pull{PlainHTTP: true, Concurrency: 1, ExtractFromRemote: true, ExtractDir: out, DisableProgress: true}

	err := (&backend{}).Pull(context.Background(), r.repo()+"@"+r.manifestDigest.String(), cfg)

	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(out, "weights.bin"))
	require.NoError(t, err)
	assert.Equal(t, digestRefFile, string(got))
}

func TestPullTagWithWrongDigestIsRefused(t *testing.T) {
	r := newDigestRegistry(t)
	cfg := &config.Pull{PlainHTTP: true, Concurrency: 1, ExtractFromRemote: true, ExtractDir: t.TempDir(), DisableProgress: true}

	err := (&backend{}).Pull(context.Background(), r.repo()+":latest@"+wrongDigest.String(), cfg)

	require.Error(t, err)
	assert.NotContains(t, r.asked, "latest")
}

// The Dragonfly paths fetch the manifest the same way before they reach
// Dragonfly, so an unreachable endpoint still shows which step failed: a
// digest-only reference must get past the manifest, and a wrong digest
// must not.
const noDragonfly = "127.0.0.1:1"

func TestDragonflyPathsFetchTheManifestByDigest(t *testing.T) {
	r := newDigestRegistry(t)
	ctx := context.Background()
	byDigest := r.repo() + "@" + r.manifestDigest.String()

	pullErr := (&backend{}).Pull(ctx, byDigest, &config.Pull{PlainHTTP: true, Concurrency: 1, DragonflyEndpoint: noDragonfly, DisableProgress: true})
	fetchErr := (&backend{}).Fetch(ctx, byDigest, &config.Fetch{Output: t.TempDir(), Patterns: []string{"*"}, PlainHTTP: true, Concurrency: 1, DragonflyEndpoint: noDragonfly})

	for name, err := range map[string]error{"pull": pullErr, "fetch": fetchErr} {
		if err != nil {
			assert.NotContains(t, err.Error(), "invalid reference", "%s: the manifest was fetched by an empty tag", name)
		}
	}
	assert.Contains(t, r.asked, r.manifestDigest.String())
}

func TestDragonflyPathsRefuseAWrongDigest(t *testing.T) {
	r := newDigestRegistry(t)
	ctx := context.Background()
	wrong := r.repo() + ":latest@" + wrongDigest.String()

	pullErr := (&backend{}).Pull(ctx, wrong, &config.Pull{PlainHTTP: true, Concurrency: 1, DragonflyEndpoint: noDragonfly, DisableProgress: true})
	fetchErr := (&backend{}).Fetch(ctx, wrong, &config.Fetch{Output: t.TempDir(), Patterns: []string{"*"}, PlainHTTP: true, Concurrency: 1, DragonflyEndpoint: noDragonfly})

	for name, err := range map[string]error{"pull": pullErr, "fetch": fetchErr} {
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "manifest", "%s: refused at the manifest, before Dragonfly", name)
	}
	assert.NotContains(t, r.asked, "latest")
}
