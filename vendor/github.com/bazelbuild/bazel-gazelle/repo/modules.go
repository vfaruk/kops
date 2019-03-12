/* Copyright 2018 The Bazel Authors. All rights reserved.

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

package repo

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/label"
)

type module struct {
	Path, Version string
	Main          bool
}

// Per the `go help modules` documentation:
//   There are three pseudo-version forms:
//
//   vX.0.0-yyyymmddhhmmss-abcdefabcdef is used when there is no earlier
//   versioned commit with an appropriate major version before the target commit.
//   (This was originally the only form, so some older go.mod files use this form
//   even for commits that do follow tags.)
//
//   vX.Y.Z-pre.0.yyyymmddhhmmss-abcdefabcdef is used when the most
//   recent versioned commit before the target commit is vX.Y.Z-pre.
//
//   vX.Y.(Z+1)-0.yyyymmddhhmmss-abcdefabcdef is used when the most
//   recent versioned commit before the target commit is vX.Y.Z.
//
// We need to match all three of these with the following regexp.

var regexMixedVersioning = regexp.MustCompile(`^(.*?)[-.]((?:0\.|)[0-9]{14})-([a-fA-F0-9]{12})$`)

func toRepoRule(mod module) Repo {
	var tag, commit string

	if gr := regexMixedVersioning.FindStringSubmatch(mod.Version); gr != nil {
		commit = gr[3]
	} else {
		tag = strings.TrimSuffix(mod.Version, "+incompatible")
	}

	return Repo{
		Name:     label.ImportPathToBazelRepoName(mod.Path),
		GoPrefix: mod.Path,
		Commit:   commit,
		Tag:      tag,
	}
}

func importRepoRulesModules(filename string, _ *RemoteCache) (repos []Repo, err error) {
	tempDir, err := copyGoModToTemp(filename)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	data, err := goListModulesFn(tempDir)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var mod module
		if err := dec.Decode(&mod); err != nil {
			return nil, err
		}
		if mod.Main {
			continue
		}

		repos = append(repos, toRepoRule(mod))
	}

	return repos, nil
}

// goListModulesFn may be overridden by tests.
var goListModulesFn = goListModules

// goListModules invokes "go list" in a directory containing a go.mod file.
func goListModules(dir string) ([]byte, error) {
	goTool := findGoTool()
	cmd := exec.Command(goTool, "list", "-m", "-json", "all")
	cmd.Stderr = os.Stderr
	cmd.Dir = dir
	data, err := cmd.Output()
	return data, err
}

// copyGoModToTemp copies to given go.mod file to a temporary directory.
// go list tends to mutate go.mod files, but gazelle shouldn't do that.
func copyGoModToTemp(filename string) (tempDir string, err error) {
	goModOrig, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer goModOrig.Close()

	tempDir, err = ioutil.TempDir("", "gazelle-temp-gomod")
	if err != nil {
		return "", err
	}

	goModCopy, err := os.Create(filepath.Join(tempDir, "go.mod"))
	if err != nil {
		os.Remove(tempDir)
		return "", err
	}
	defer func() {
		if cerr := goModCopy.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	_, err = io.Copy(goModCopy, goModOrig)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", err
	}
	return tempDir, err
}

// findGoTool attempts to locate the go executable. If GOROOT is set, we'll
// prefer the one in there; otherwise, we'll rely on PATH. If the wrapper
// script generated by the gazelle rule is invoked by Bazel, it will set
// GOROOT to the configured SDK. We don't want to rely on the host SDK in
// that situation.
func findGoTool() string {
	path := "go" // rely on PATH by default
	if goroot, ok := os.LookupEnv("GOROOT"); ok {
		path = filepath.Join(goroot, "bin", "go")
	}
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	return path
}
