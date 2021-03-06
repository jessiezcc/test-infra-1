/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package adapter

import (
	"sync"
	"testing"
	"time"

	"k8s.io/test-infra/prow/gerrit/client"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
)

type fca struct {
	sync.Mutex
	c *config.Config
}

func (f *fca) Config() *config.Config {
	f.Lock()
	defer f.Unlock()
	return f.c
}

type fkc struct {
	sync.Mutex
	prowjobs []prowapi.ProwJob
}

func (f *fkc) CreateProwJob(pj prowapi.ProwJob) (prowapi.ProwJob, error) {
	f.Lock()
	defer f.Unlock()
	f.prowjobs = append(f.prowjobs, pj)
	return pj, nil
}

type fgc struct{}

func (f *fgc) QueryChanges(lastUpdate time.Time, rateLimit int) map[string][]client.ChangeInfo {
	return nil
}

func (f *fgc) SetReview(instance, id, revision, message string, labels map[string]string) error {
	return nil
}

func (f *fgc) GetBranchRevision(instance, project, branch string) (string, error) {
	return "abc", nil
}

func TestMakeCloneURI(t *testing.T) {
	cases := []struct {
		name     string
		instance string
		project  string
		expected string
		err      bool
	}{
		{
			name:     "happy case",
			instance: "https://android.googlesource.com",
			project:  "platform/build",
			expected: "https://android.googlesource.com/platform/build",
		},
		{
			name:     "reject non urls",
			instance: "!!!://",
			project:  "platform/build",
			err:      true,
		},
		{
			name:     "require instance to specify host",
			instance: "android.googlesource.com",
			project:  "platform/build",
			err:      true,
		},
		{
			name:     "reject instances with paths",
			instance: "https://android.googlesource.com/platform",
			project:  "build",
			err:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := makeCloneURI(tc.instance, tc.project)
			switch {
			case err != nil:
				if !tc.err {
					t.Errorf("unexpected error: %v", err)
				}
			case tc.err:
				t.Error("failed to receive expected exception")
			case actual.String() != tc.expected:
				t.Errorf("actual %q != expected %q", actual.String(), tc.expected)
			}
		})
	}
}

func TestProcessChange(t *testing.T) {
	var testcases = []struct {
		name        string
		change      client.ChangeInfo
		numPJ       int
		pjRef       string
		shouldError bool
	}{
		{
			name: "no revisions errors out",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Status:          "NEW",
			},
			shouldError: true,
		},
		{
			name: "wrong project triggers no jobs",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "woof",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {},
				},
			},
		},
		{
			name: "normal changes should trigger matching branch jobs",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Ref: "refs/changes/00/1/1",
					},
				},
			},
			numPJ: 2,
			pjRef: "refs/changes/00/1/1",
		},
		{
			name: "multiple revisions",
			change: client.ChangeInfo{
				CurrentRevision: "2",
				Project:         "test-infra",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Ref: "refs/changes/00/2/1",
					},
					"2": {
						Ref: "refs/changes/00/2/2",
					},
				},
			},
			numPJ: 2,
			pjRef: "refs/changes/00/2/2",
		},
		{
			name: "other-test-with-https",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "other-repo",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Ref: "refs/changes/00/1/1",
					},
				},
			},
			numPJ: 1,
			pjRef: "refs/changes/00/1/1",
		},
		{
			name: "merged change should trigger postsubmit",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "postsubmits-project",
				Status:          "MERGED",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Ref: "refs/changes/00/1/1",
					},
				},
			},
			numPJ: 1,
			pjRef: "refs/changes/00/1/1",
		},
		{
			name: "merged change on project without postsubmits",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Status:          "MERGED",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Ref: "refs/changes/00/1/1",
					},
				},
			},
		},
		{
			name: "presubmit runs when a file matches run_if_changed",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Files: map[string]client.FileInfo{
							"bee-movie-script.txt": {},
							"africa-lyrics.txt":    {},
							"important-code.go":    {},
						},
					},
				},
			},
			numPJ: 3,
		},
		{
			name: "presubmit doesn't run when no files match run_if_changed",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {
						Files: map[string]client.FileInfo{
							"hacky-hack.sh": {},
							"README.md":     {},
							"let-it-go.txt": {},
						},
					},
				},
			},
			numPJ: 2,
		},
		{
			name: "presubmit run when change against matched branch",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Branch:          "pony",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {},
				},
			},
			numPJ: 3,
		},
		{
			name: "presubmit doesn't run when not against target branch",
			change: client.ChangeInfo{
				CurrentRevision: "1",
				Project:         "test-infra",
				Branch:          "baz",
				Status:          "NEW",
				Revisions: map[string]client.RevisionInfo{
					"1": {},
				},
			},
			numPJ: 1,
		},
	}

	for _, tc := range testcases {
		testInfraPresubmits := []config.Presubmit{
			{
				JobBase: config.JobBase{
					Name: "always-runs-all-branches",
				},
				AlwaysRun: true,
			},
			{
				JobBase: config.JobBase{
					Name: "run-if-changed-all-branches",
				},
				RegexpChangeMatcher: config.RegexpChangeMatcher{
					RunIfChanged: "\\.go",
				},
			},
			{
				JobBase: config.JobBase{
					Name: "runs-on-pony-branch",
				},
				Brancher: config.Brancher{
					Branches: []string{"pony"},
				},
				AlwaysRun: true,
			},
			{
				JobBase: config.JobBase{
					Name: "runs-on-all-but-baz-branch",
				},
				Brancher: config.Brancher{
					SkipBranches: []string{"baz"},
				},
				AlwaysRun: true,
			},
		}
		if err := config.SetPresubmitRegexes(testInfraPresubmits); err != nil {
			t.Fatalf("could not set regexes: %v", err)
		}

		fca := &fca{
			c: &config.Config{
				JobConfig: config.JobConfig{
					Presubmits: map[string][]config.Presubmit{
						"gerrit/test-infra": testInfraPresubmits,
						"https://gerrit/other-repo": {
							{
								JobBase: config.JobBase{
									Name: "other-test",
								},
								AlwaysRun: true,
							},
						},
					},
					Postsubmits: map[string][]config.Postsubmit{
						"gerrit/postsubmits-project": {
							{
								JobBase: config.JobBase{
									Name: "test-bar",
								},
							},
						},
					},
				},
			},
		}

		fkc := &fkc{}

		c := &Controller{
			config: fca.Config,
			kc:     fkc,
			gc:     &fgc{},
		}

		err := c.ProcessChange("https://gerrit", tc.change)
		if err != nil && !tc.shouldError {
			t.Errorf("tc %s, expect no error, but got %v", tc.name, err)
			continue
		} else if err == nil && tc.shouldError {
			t.Errorf("tc %s, expect error, but got none", tc.name)
			continue
		}

		if len(fkc.prowjobs) != tc.numPJ {
			t.Errorf("tc %s - should make %d prowjob, got %d", tc.name, tc.numPJ, len(fkc.prowjobs))
		}

		if len(fkc.prowjobs) > 0 {
			refs := fkc.prowjobs[0].Spec.Refs
			if refs.Org != "gerrit" {
				t.Errorf("%s: org %s != gerrit", tc.name, refs.Org)
			}
			if refs.Repo != tc.change.Project {
				t.Errorf("%s: repo %s != expected %s", tc.name, refs.Repo, tc.change.Project)
			}
			if fkc.prowjobs[0].Spec.Refs.Pulls[0].Ref != tc.pjRef {
				t.Errorf("tc %s - ref should be %s, got %s", tc.name, tc.pjRef, fkc.prowjobs[0].Spec.Refs.Pulls[0].Ref)
			}
			if fkc.prowjobs[0].Spec.Refs.BaseSHA != "abc" {
				t.Errorf("tc %s - BaseSHA should be abc, got %s", tc.name, fkc.prowjobs[0].Spec.Refs.BaseSHA)
			}
		}
	}
}
