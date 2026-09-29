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

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// manifestFetcher is the part of a registry's manifest store a pull needs.
type manifestFetcher interface {
	FetchReference(ctx context.Context, reference string) (ocispec.Descriptor, io.ReadCloser, error)
}

// fetchManifest fetches the manifest a reference names and returns its
// descriptor and bytes.
//
// A reference that carries a digest is fetched BY that digest: a digest pins
// the content and a tag beside it (`repo:tag@sha256:...`) is only a name. The
// bytes are then hashed and must be the digest asked for, so a caller who
// pinned one artifact cannot be handed another -- by a registry, a proxy, or
// a tag that moved. Only a reference without a digest is fetched by tag.
func fetchManifest(ctx context.Context, store manifestFetcher, ref Referencer) (ocispec.Descriptor, []byte, error) {
	pinned := ref.Digest()
	query := pinned
	if query == "" {
		query = ref.Tag()
	}

	desc, reader, err := store.FetchReference(ctx, query)
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("failed to fetch the manifest: %w", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("failed to read the manifest: %w", err)
	}

	if pinned != "" {
		want := godigest.Digest(pinned)
		if got := want.Algorithm().FromBytes(body); got != want || desc.Digest != want {
			return ocispec.Descriptor{}, nil, fmt.Errorf("the manifest fetched is %s, not the %s the reference pins", got, want)
		}
	}

	return desc, body, nil
}

// storeReference is what a manifest is stored under locally: its tag, or
// nothing when the reference is a digest alone -- the store keys every
// manifest by its digest already, and a digest is not a tag.
func storeReference(ref Referencer) string {
	return ref.Tag()
}
