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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelpack/modctl/pkg/modelfile"
)

// --no-creation-time promises repeated builds, and two builds of the same
// files still differed: the Modelfile embedded in the manifest annotation
// began with "# Generated at <now>". Without a creation time, no clock is
// read into the manifest; with one, the line stays as it was.
func TestManifestAnnotationReadsNoClockWithoutCreationTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Modelfile")
	require.NoError(t, os.WriteFile(path, []byte("NAME m\nMODEL model.safetensors\n"), 0o644))
	mf, err := modelfile.NewModelfile(path)
	require.NoError(t, err)

	assert.NotContains(t, manifestAnnotation(mf, true)[annotationModelfile], "Generated at")
	assert.Contains(t, manifestAnnotation(mf, true)[annotationModelfile], "NAME m", "the Modelfile itself is still there")
	assert.Contains(t, manifestAnnotation(mf, false)[annotationModelfile], "# Generated at ")
}
