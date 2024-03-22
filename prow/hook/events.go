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

package hook

import (
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
)

const FailedCommentCoerceFmt = "Could not coerce %s event to a GenericCommentEvent. Unknown 'action': %q."

const eventTypeField = "event-type"

var (
	nonCommentIssueActions = map[github.IssueEventAction]bool{
		github.IssueActionAssigned:     true,
		github.IssueActionUnassigned:   true,
		github.IssueActionLabeled:      true,
		github.IssueActionUnlabeled:    true,
		github.IssueActionMilestoned:   true,
		github.IssueActionDemilestoned: true,
		github.IssueActionClosed:       true,
		github.IssueActionReopened:     true,
		github.IssueActionPinned:       true,
		github.IssueActionUnpinned:     true,
		github.IssueActionTransferred:  true,
		github.IssueActionDeleted:      true,
		github.IssueActionLocked:       true,
		github.IssueActionUnlocked:     true,
	}
	nonCommentPullRequestActions = map[github.PullRequestEventAction]bool{
		github.PullRequestActionAssigned:             true,
		github.PullRequestActionUnassigned:           true,
		github.PullRequestActionReviewRequested:      true,
		github.PullRequestActionReviewRequestRemoved: true,
		github.PullRequestActionLabeled:              true,
		github.PullRequestActionUnlabeled:            true,
		github.PullRequestActionClosed:               true,
		github.PullRequestActionReopened:             true,
		github.PullRequestActionSynchronize:          true,
		github.PullRequestActionReadyForReview:       true,
		github.PullRequestActionConvertedToDraft:     true,
		github.PullRequestActionLocked:               true,
		github.PullRequestActionUnlocked:             true,
		github.PullRequestActionAutoMergeEnabled:     true,
		github.PullRequestActionAutoMergeDisabled:    true,
	}
)

func (s *Server) handleReviewEvent(l *logrus.Entry, re github.ReviewEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  re.Repo.Owner.Login,
		github.RepoLogField: re.Repo.Name,
		github.PrLogField:   re.PullRequest.Number,
		"review":            re.Review.ID,
		"reviewer":          re.Review.User.Login,
		"url":               re.Review.HTMLURL,
	})
	l.Infof("Review %s.", re.Action)
	for p, h := range s.Plugins.ReviewEventHandlers(re.PullRequest.Base.Repo.Owner.Login, re.PullRequest.Base.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.ReviewEventHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, re.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			agent.InitializeCommentPruner(
				re.Repo.Owner.Login,
				re.Repo.Name,
				re.PullRequest.Number,
			)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, re) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": string(re.Action), "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling ReviewEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
	action := github.GeneralizeCommentAction(string(re.Action))
	if action == "" {
		l.Errorf(FailedCommentCoerceFmt, "pull_request_review", string(re.Action))
		return
	}

	gce, err := github.GeneralizeComment(re)
	if err != nil {
		l.Errorln(err)
		return
	}

	s.handleGenericComment(l, gce)
}

func (s *Server) handleReviewCommentEvent(l *logrus.Entry, rce github.ReviewCommentEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  rce.Repo.Owner.Login,
		github.RepoLogField: rce.Repo.Name,
		github.PrLogField:   rce.PullRequest.Number,
		"review":            rce.Comment.ReviewID,
		"commenter":         rce.Comment.User.Login,
		"url":               rce.Comment.HTMLURL,
	})
	l.Infof("Review comment %s.", rce.Action)
	for p, h := range s.Plugins.ReviewCommentEventHandlers(rce.PullRequest.Base.Repo.Owner.Login, rce.PullRequest.Base.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.ReviewCommentEventHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, rce.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			agent.InitializeCommentPruner(
				rce.Repo.Owner.Login,
				rce.Repo.Name,
				rce.PullRequest.Number,
			)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, rce) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": string(rce.Action), "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling ReviewCommentEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
	action := github.GeneralizeCommentAction(string(rce.Action))
	if action == "" {
		l.Errorf(FailedCommentCoerceFmt, "pull_request_review_comment", string(rce.Action))
		return
	}

	gce, err := github.GeneralizeComment(rce)
	if err != nil {
		l.Errorln(err)
		return
	}

	s.handleGenericComment(l, gce)
}

func (s *Server) handlePullRequestEvent(l *logrus.Entry, pr github.PullRequestEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  pr.Repo.Owner.Login,
		github.RepoLogField: pr.Repo.Name,
		github.PrLogField:   pr.Number,
		"author":            pr.PullRequest.User.Login,
		"url":               pr.PullRequest.HTMLURL,
	})
	l.Infof("Pull request %s.", pr.Action)
	for p, h := range s.Plugins.PullRequestHandlers(pr.PullRequest.Base.Repo.Owner.Login, pr.PullRequest.Base.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.PullRequestHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, pr.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			agent.InitializeCommentPruner(
				pr.Repo.Owner.Login,
				pr.Repo.Name,
				pr.PullRequest.Number,
			)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, pr) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": string(pr.Action), "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling PullRequestEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
	action := github.GeneralizeCommentAction(string(pr.Action))
	if action == "" {
		if !nonCommentPullRequestActions[pr.Action] {
			l.Infof(FailedCommentCoerceFmt, "pull_request", string(pr.Action))
		}
		return
	}

	gce, err := github.GeneralizeComment(pr)
	if err != nil {
		l.Errorln(err)
		return
	}

	s.handleGenericComment(l, gce)
}

func (s *Server) handlePushEvent(l *logrus.Entry, pe github.PushEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  pe.Repo.Owner.Name,
		github.RepoLogField: pe.Repo.Name,
		"ref":               pe.Ref,
		"head":              pe.After,
	})
	l.Info("Push event.")
	for p, h := range s.Plugins.PushEventHandlers(pe.Repo.Owner.Name, pe.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.PushEventHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, pe.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, pe) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": "none", "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling PushEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
}

func (s *Server) handleIssueEvent(l *logrus.Entry, i github.IssueEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  i.Repo.Owner.Login,
		github.RepoLogField: i.Repo.Name,
		github.PrLogField:   i.Issue.Number,
		"author":            i.Issue.User.Login,
		"url":               i.Issue.HTMLURL,
	})
	l.Infof("Issue %s.", i.Action)
	for p, h := range s.Plugins.IssueHandlers(i.Repo.Owner.Login, i.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.IssueHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, i.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			agent.InitializeCommentPruner(
				i.Repo.Owner.Login,
				i.Repo.Name,
				i.Issue.Number,
			)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, i) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": string(i.Action), "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling IssueEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
	action := github.GeneralizeCommentAction(string(i.Action))
	if action == "" {
		if !nonCommentIssueActions[i.Action] {
			l.Errorf(FailedCommentCoerceFmt, "issues", string(i.Action))
		}
		return
	}

	gce, err := github.GeneralizeComment(i)
	if err != nil {
		l.Errorln(err)
		return
	}

	s.handleGenericComment(l, gce)
}

func (s *Server) handleIssueCommentEvent(l *logrus.Entry, ic github.IssueCommentEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  ic.Repo.Owner.Login,
		github.RepoLogField: ic.Repo.Name,
		github.PrLogField:   ic.Issue.Number,
		"author":            ic.Comment.User.Login,
		"url":               ic.Comment.HTMLURL,
	})
	l.Infof("Issue comment %s.", ic.Action)
	for p, h := range s.Plugins.IssueCommentHandlers(ic.Repo.Owner.Login, ic.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.IssueCommentHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, ic.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			agent.InitializeCommentPruner(
				ic.Repo.Owner.Login,
				ic.Repo.Name,
				ic.Issue.Number,
			)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, ic) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": string(ic.Action), "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling IssueCommentEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
	action := github.GeneralizeCommentAction(string(ic.Action))
	if action == "" {
		l.Errorf(FailedCommentCoerceFmt, "issue_comment", string(ic.Action))
		return
	}

	gce, err := github.GeneralizeComment(ic)
	if err != nil {
		l.Errorln(err)
		return
	}

	s.handleGenericComment(l, gce)
}

func (s *Server) handleStatusEvent(l *logrus.Entry, se github.StatusEvent) {
	defer s.wg.Done()
	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  se.Repo.Owner.Login,
		github.RepoLogField: se.Repo.Name,
		"context":           se.Context,
		"sha":               se.SHA,
		"state":             se.State,
		"id":                se.ID,
	})
	l.Infof("Status description %s.", se.Description)
	for p, h := range s.Plugins.StatusEventHandlers(se.Repo.Owner.Login, se.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.StatusEventHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, se.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, se) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": "none", "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling StatusEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
}

func (s *Server) handleGenericComment(l *logrus.Entry, ce *github.GenericCommentEvent) {
	for p, h := range s.Plugins.GenericCommentHandlers(ce.Repo.Owner.Login, ce.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.GenericCommentHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ConfigAgent, s.Plugins, s.ClientAgent, ce.Repo.Owner.Login, s.Metrics.Metrics, l, p)
			agent.InitializeCommentPruner(
				ce.Repo.Owner.Login,
				ce.Repo.Name,
				ce.Number,
			)
			start := time.Now()
			err := errorOnPanic(func() error { return h(agent, *ce) })
			labels := prometheus.Labels{"event_type": l.Data[eventTypeField].(string), "action": string(ce.Action), "plugin": p, "took_action": strconv.FormatBool(agent.TookAction())}
			if err != nil {
				agent.Logger.WithError(err).Error("Error handling GenericCommentEvent.")
				s.Metrics.PluginHandleErrors.With(labels).Inc()
			}
			s.Metrics.PluginHandleDuration.With(labels).Observe(time.Since(start).Seconds())
		}(p, h)
	}
}

func errorOnPanic(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic caught: %v. stack is: %s", r, debug.Stack())
		}
	}()
	return f()
}
