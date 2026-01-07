/*
Copyright 2016 The Kubernetes Authors.

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

package pluginimports

// We need to empty import all enabled plugins so that they will be linked into
// any hook binary.
import (
	_ "sigs.k8s.io/prow/pkg/plugins/approve" // Import all enabled plugins.
	_ "sigs.k8s.io/prow/pkg/plugins/assign"
	_ "sigs.k8s.io/prow/pkg/plugins/blockade"
	_ "sigs.k8s.io/prow/pkg/plugins/blunderbuss"
	_ "sigs.k8s.io/prow/pkg/plugins/branchcleaner"
	_ "sigs.k8s.io/prow/pkg/plugins/bugzilla"
	_ "sigs.k8s.io/prow/pkg/plugins/buildifier"
	_ "sigs.k8s.io/prow/pkg/plugins/cat"
	_ "sigs.k8s.io/prow/pkg/plugins/cherrypickapproved"
	_ "sigs.k8s.io/prow/pkg/plugins/cherrypickunapproved"
	_ "sigs.k8s.io/prow/pkg/plugins/cla"
	_ "sigs.k8s.io/prow/pkg/plugins/dog"
	_ "sigs.k8s.io/prow/pkg/plugins/golint"
	_ "sigs.k8s.io/prow/pkg/plugins/goose"
	_ "sigs.k8s.io/prow/pkg/plugins/heart"
	_ "sigs.k8s.io/prow/pkg/plugins/help"
	_ "sigs.k8s.io/prow/pkg/plugins/hold"
	_ "sigs.k8s.io/prow/pkg/plugins/invalidcommitmsg"
	_ "sigs.k8s.io/prow/pkg/plugins/jira"
	_ "sigs.k8s.io/prow/pkg/plugins/label"
	_ "sigs.k8s.io/prow/pkg/plugins/lgtm"
	_ "sigs.k8s.io/prow/pkg/plugins/lifecycle"
	_ "sigs.k8s.io/prow/pkg/plugins/merge-method-comment"
	_ "sigs.k8s.io/prow/pkg/plugins/mergecommitblocker"
	_ "sigs.k8s.io/prow/pkg/plugins/milestone"
	_ "sigs.k8s.io/prow/pkg/plugins/milestoneapplier"
	_ "sigs.k8s.io/prow/pkg/plugins/milestonestatus"
	_ "sigs.k8s.io/prow/pkg/plugins/override"
	_ "sigs.k8s.io/prow/pkg/plugins/owners-label"
	_ "sigs.k8s.io/prow/pkg/plugins/pony"
	_ "sigs.k8s.io/prow/pkg/plugins/project"
	_ "sigs.k8s.io/prow/pkg/plugins/projectmanager"
	_ "sigs.k8s.io/prow/pkg/plugins/releasenote"
	_ "sigs.k8s.io/prow/pkg/plugins/require-matching-label"
	_ "sigs.k8s.io/prow/pkg/plugins/retitle"
	_ "sigs.k8s.io/prow/pkg/plugins/shrug"
	_ "sigs.k8s.io/prow/pkg/plugins/sigmention"
	_ "sigs.k8s.io/prow/pkg/plugins/size"
	_ "sigs.k8s.io/prow/pkg/plugins/skip"
	_ "sigs.k8s.io/prow/pkg/plugins/slackevents"
	_ "sigs.k8s.io/prow/pkg/plugins/stage"
	_ "sigs.k8s.io/prow/pkg/plugins/testfreeze"
	_ "sigs.k8s.io/prow/pkg/plugins/transfer-issue"
	_ "sigs.k8s.io/prow/pkg/plugins/trick-or-treat"
	_ "sigs.k8s.io/prow/pkg/plugins/trigger"
	_ "sigs.k8s.io/prow/pkg/plugins/updateconfig"
	_ "sigs.k8s.io/prow/pkg/plugins/verify-owners"
	_ "sigs.k8s.io/prow/pkg/plugins/welcome"
	_ "sigs.k8s.io/prow/pkg/plugins/wip"
	_ "sigs.k8s.io/prow/pkg/plugins/yuks"
)
