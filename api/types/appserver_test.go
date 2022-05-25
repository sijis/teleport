/**
 * Copyright 2022 Gravitational, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeduplicateAppServers(t *testing.T) {
	t.Parallel()

	expected := []AppServer{
		&AppServerV3{Metadata: Metadata{Name: "s1"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "a"}}}},
		&AppServerV3{Metadata: Metadata{Name: "s2"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "a", Labels: map[string]string{"env": "dev"}}}}},
		&AppServerV3{Metadata: Metadata{Name: "s3"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "a", Labels: map[string]string{"env": "prod"}}}}},
		&AppServerV3{Metadata: Metadata{Name: "s4"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "b"}}}},
		&AppServerV3{Metadata: Metadata{Name: "s5"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "b"}, Spec: AppSpecV3{PublicAddr: "test"}}}},
		&AppServerV3{Metadata: Metadata{Name: "s6"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "c"}}}},
	}

	dups := []AppServer{
		&AppServerV3{Metadata: Metadata{Name: "s7"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "a", Labels: map[string]string{"env": "prod"}}}}},
		&AppServerV3{Metadata: Metadata{Name: "s8"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "b"}, Spec: AppSpecV3{PublicAddr: "test"}}}},
		&AppServerV3{Metadata: Metadata{Name: "s9"}, Spec: AppServerSpecV3{App: &AppV3{Metadata: Metadata{Name: "c"}}}},
	}

	servers := append(expected, dups...)

	result := DeduplicateAppServers(servers)
	require.ElementsMatch(t, result, expected)
}
