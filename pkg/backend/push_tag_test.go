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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalpb "github.com/modelpack/modctl/internal/pb"
	"github.com/modelpack/modctl/pkg/backend/remote"
	"github.com/modelpack/modctl/pkg/iometrics"
)

// immutableTagRegistry holds two manifests. The tag "v1" points at the old
// one and cannot be moved -- Harbor's immutable tags answer 412 -- while
// the new one already exists by digest, as it does after a first attempt
// pushed it and then failed to move the tag.
type immutableTagRegistry struct {
	server              *httptest.Server
	oldBody, newBody    []byte
	oldDigest, newDigst godigest.Digest
	tagMoves            int
}

func newImmutableTagRegistry(t *testing.T) *immutableTagRegistry {
	t.Helper()
	r := &immutableTagRegistry{
		oldBody: []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.cncf.model.config.v1+json","digest":"sha256:aaaa","size":1},"layers":[]}`),
		newBody: []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.cncf.model.config.v1+json","digest":"sha256:bbbb","size":1},"layers":[]}`),
	}
	r.oldDigest, r.newDigst = godigest.FromBytes(r.oldBody), godigest.FromBytes(r.newBody)
	serve := func(w http.ResponseWriter, req *http.Request, body []byte, d godigest.Digest) {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
		w.Header().Set("Docker-Content-Digest", d.String())
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if req.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		const prefix = "/v2/test/model/manifests/"
		if req.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if !strings.HasPrefix(req.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ref := strings.TrimPrefix(req.URL.Path, prefix)
		switch {
		case req.Method == http.MethodPut && ref == "v1":
			_, _ = io.Copy(io.Discard, req.Body)
			r.tagMoves++
			w.WriteHeader(http.StatusPreconditionFailed) // the tag is immutable
		case ref == "v1" || ref == r.oldDigest.String():
			serve(w, req, r.oldBody, r.oldDigest)
		case ref == r.newDigst.String():
			serve(w, req, r.newBody, r.newDigst)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

// Pushing new content to an immutable tag reported success: the retry
// found the manifest by digest and the tag by name, and never asked whether
// the tag pointed at this manifest. The tag still names the old content.
func TestPushToATagThatWillNotMoveFails(t *testing.T) {
	r := newImmutableTagRegistry(t)
	dst, err := remote.New(strings.TrimPrefix(r.server.URL, "http://")+"/test/model", remote.WithPlainHTTP(true))
	require.NoError(t, err)
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: r.newDigst, Size: int64(len(r.newBody)), Data: r.newBody}

	err = pushIfNotExist(context.Background(), internalpb.NewProgressBar(io.Discard), "Copying manifest", nil, dst, desc, "test/model", "v1", iometrics.NewTracker("push"))

	require.Error(t, err, "the tag still points at %s", r.oldDigest)
	assert.Contains(t, err.Error(), "v1")
	assert.Equal(t, 1, r.tagMoves, "the tag was asked to move, and refused")
}
