/*
 *     Copyright 2026 The ModelPack Authors
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

package distribution

import (
	"bytes"
	"context"
	"errors"
	"testing"

	distribution "github.com/distribution/distribution/v3"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPushBlobPropagatesCommitError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)

	_, _, err = storage.PushBlob(context.Background(), "example.com/test", bytes.NewBufferString("blob"), ocispec.Descriptor{
		Digest: godigest.FromString("different blob"),
		Size:   4,
	})

	var invalidDigest distribution.ErrBlobInvalidDigest
	require.True(t, errors.As(err, &invalidDigest))
}

// A manifest pulled by digest alone has no tag, and PushManifest tagged it
// with whatever reference it was given -- an empty one, or the digest
// itself. It is stored under its digest and tagged with nothing.
func TestPushManifestByDigestStoresWithoutATag(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.cncf.model.config.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[]}`)
	digest := godigest.FromBytes(body)
	config := []byte("{}")
	_, _, err = storage.PushBlob(ctx, "example.com/test", bytes.NewReader(config), ocispec.Descriptor{Digest: godigest.FromBytes(config), Size: int64(len(config))})
	require.NoError(t, err, "the config the manifest names")

	for _, reference := range []string{"", digest.String()} {
		got, err := storage.PushManifest(ctx, "example.com/test", reference, body)
		require.NoError(t, err, "reference %q", reference)
		require.Equal(t, digest.String(), got)
	}

	exists, err := storage.StatManifest(ctx, "example.com/test", digest.String())
	require.NoError(t, err)
	require.True(t, exists, "the manifest is stored under its digest")
	tags, err := storage.ListTags(ctx, "example.com/test")
	require.NoError(t, err)
	require.Empty(t, tags, "and no tag was made from a digest or from nothing")
}
