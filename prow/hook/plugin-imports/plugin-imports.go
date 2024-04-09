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
	_ "sigs.k8s.io/prow/prow/plugins/approve" // Import all enabled plugins.
	_ "sigs.k8s.io/prow/prow/plugins/assign"
	_ "sigs.k8s.io/prow/prow/plugins/blockade"
	_ "sigs.k8s.io/prow/prow/plugins/blunderbuss"
	_ "sigs.k8s.io/prow/prow/plugins/branchcleaner"
	_ "sigs.k8s.io/prow/prow/plugins/bugzilla"
	_ "sigs.k8s.io/prow/prow/plugins/buildifier"
	_ "sigs.k8s.io/prow/prow/plugins/cat"
	_ "sigs.k8s.io/prow/prow/plugins/cherrypickapproved"
	_ "sigs.k8s.io/prow/prow/plugins/cherrypickunapproved"
	_ "sigs.k8s.io/prow/prow/plugins/cla"
	_ "sigs.k8s.io/prow/prow/plugins/dco"
	_ "sigs.k8s.io/prow/prow/plugins/dog"
	_ "sigs.k8s.io/prow/prow/plugins/golint"
	_ "sigs.k8s.io/prow/prow/plugins/goose"
	_ "sigs.k8s.io/prow/prow/plugins/heart"
	_ "sigs.k8s.io/prow/prow/plugins/help"
	_ "sigs.k8s.io/prow/prow/plugins/hold"
	_ "sigs.k8s.io/prow/prow/plugins/invalidcommitmsg"
	_ "sigs.k8s.io/prow/prow/plugins/jira"
	_ "sigs.k8s.io/prow/prow/plugins/label"
	_ "sigs.k8s.io/prow/prow/plugins/lgtm"
	_ "sigs.k8s.io/prow/prow/plugins/lifecycle"
	_ "sigs.k8s.io/prow/prow/plugins/merge-method-comment"
	_ "sigs.k8s.io/prow/prow/plugins/mergecommitblocker"
	_ "sigs.k8s.io/prow/prow/plugins/milestone"
	_ "sigs.k8s.io/prow/prow/plugins/milestoneapplier"
	_ "sigs.k8s.io/prow/prow/plugins/milestonestatus"
	_ "sigs.k8s.io/prow/prow/plugins/override"
	_ "sigs.k8s.io/prow/prow/plugins/owners-label"
	_ "sigs.k8s.io/prow/prow/plugins/pony"
	_ "sigs.k8s.io/prow/prow/plugins/project"
	_ "sigs.k8s.io/prow/prow/plugins/projectmanager"
	_ "sigs.k8s.io/prow/prow/plugins/releasenote"
	_ "sigs.k8s.io/prow/prow/plugins/require-matching-label"
	_ "sigs.k8s.io/prow/prow/plugins/retitle"
	_ "sigs.k8s.io/prow/prow/plugins/shrug"
	_ "sigs.k8s.io/prow/prow/plugins/sigmention"
	_ "sigs.k8s.io/prow/prow/plugins/size"
	_ "sigs.k8s.io/prow/prow/plugins/skip"
	_ "sigs.k8s.io/prow/prow/plugins/slackevents"
	_ "sigs.k8s.io/prow/prow/plugins/stage"
	_ "sigs.k8s.io/prow/prow/plugins/testfreeze"
	_ "sigs.k8s.io/prow/prow/plugins/transfer-issue"
	_ "sigs.k8s.io/prow/prow/plugins/trick-or-treat"
	_ "sigs.k8s.io/prow/prow/plugins/trigger"
	_ "sigs.k8s.io/prow/prow/plugins/updateconfig"
	_ "sigs.k8s.io/prow/prow/plugins/verify-owners"
	_ "sigs.k8s.io/prow/prow/plugins/welcome"
	_ "sigs.k8s.io/prow/prow/plugins/wip"
	_ "sigs.k8s.io/prow/prow/plugins/yuks"
)
