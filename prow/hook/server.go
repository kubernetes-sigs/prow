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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/githubeventserver"
	_ "k8s.io/test-infra/prow/hook/plugin-imports"
	"k8s.io/test-infra/prow/plugins"
)

// Server implements http.Handler. It validates incoming GitHub webhooks and
// then dispatches them to the appropriate plugins.
type Server struct {
	ClientAgent    *plugins.ClientAgent
	Plugins        *plugins.ConfigAgent
	ConfigAgent    *config.Agent
	TokenGenerator func() []byte
	Metrics        *githubeventserver.Metrics
	RepoEnabled    func(org, repo string) bool

	// c is an http client used for dispatching events
	// to external plugin services.
	c http.Client
	// Tracks running handlers for graceful shutdown
	wg sync.WaitGroup
}

// ServeHTTP validates an incoming webhook and puts it into the event channel.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, resp := github.ValidateWebhook(w, r, s.TokenGenerator)
	if counter, err := s.Metrics.ResponseCounter.GetMetricWithLabelValues(strconv.Itoa(resp)); err != nil {
		logrus.WithFields(logrus.Fields{
			"status-code": resp,
		}).WithError(err).Error("Failed to get metric for reporting webhook status code")
	} else {
		counter.Inc()
	}

	if !ok {
		return
	}
	fmt.Fprint(w, "Event received. Have a nice day.")

	if err := s.demuxEvent(eventType, eventGUID, payload, r.Header); err != nil {
		logrus.WithError(err).Error("Error parsing event.")
	}
}

func (s *Server) demuxEvent(eventType, eventGUID string, payload []byte, h http.Header) error {
	l := logrus.WithFields(
		logrus.Fields{
			eventTypeField:   eventType,
			github.EventGUID: eventGUID,
		},
	)
	// We don't want to fail the webhook due to a metrics error.
	if counter, err := s.Metrics.WebhookCounter.GetMetricWithLabelValues(eventType); err != nil {
		l.WithError(err).Warn("Failed to get metric for eventType " + eventType)
	} else {
		counter.Inc()
	}
	var srcRepo string
	switch eventType {
	case "issues":
		var i github.IssueEvent
		if err := json.Unmarshal(payload, &i); err != nil {
			return err
		}
		i.GUID = eventGUID
		srcRepo = i.Repo.FullName
		if s.RepoEnabled(i.Repo.Owner.Login, i.Repo.Name) {
			s.wg.Add(1)
			go s.handleIssueEvent(l, i)
		}
	case "issue_comment":
		var ic github.IssueCommentEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		ic.GUID = eventGUID
		srcRepo = ic.Repo.FullName
		if s.RepoEnabled(ic.Repo.Owner.Login, ic.Repo.Name) {
			s.wg.Add(1)
			go s.handleIssueCommentEvent(l, ic)
		}
	case "pull_request":
		var pr github.PullRequestEvent
		if err := json.Unmarshal(payload, &pr); err != nil {
			return err
		}
		pr.GUID = eventGUID
		srcRepo = pr.Repo.FullName
		if s.RepoEnabled(pr.Repo.Owner.Login, pr.Repo.Name) {
			s.wg.Add(1)
			go s.handlePullRequestEvent(l, pr)
		}
	case "pull_request_review":
		var re github.ReviewEvent
		if err := json.Unmarshal(payload, &re); err != nil {
			return err
		}
		re.GUID = eventGUID
		srcRepo = re.Repo.FullName
		if s.RepoEnabled(re.Repo.Owner.Login, re.Repo.Name) {
			s.wg.Add(1)
			go s.handleReviewEvent(l, re)
		}
	case "pull_request_review_comment":
		var rce github.ReviewCommentEvent
		if err := json.Unmarshal(payload, &rce); err != nil {
			return err
		}
		rce.GUID = eventGUID
		srcRepo = rce.Repo.FullName
		if s.RepoEnabled(rce.Repo.Owner.Login, rce.Repo.Name) {
			s.wg.Add(1)
			go s.handleReviewCommentEvent(l, rce)
		}
	case "push":
		var pe github.PushEvent
		if err := json.Unmarshal(payload, &pe); err != nil {
			return err
		}
		pe.GUID = eventGUID
		srcRepo = pe.Repo.FullName
		if s.RepoEnabled(pe.Repo.Owner.Login, pe.Repo.Name) {
			s.wg.Add(1)
			go s.handlePushEvent(l, pe)
		}
	case "status":
		var se github.StatusEvent
		if err := json.Unmarshal(payload, &se); err != nil {
			return err
		}
		se.GUID = eventGUID
		srcRepo = se.Repo.FullName
		if s.RepoEnabled(se.Repo.Owner.Login, se.Repo.Name) {
			s.wg.Add(1)
			go s.handleStatusEvent(l, se)
		}
	default:
		var ge github.GenericEvent
		if err := json.Unmarshal(payload, &ge); err != nil {
			return err
		}
		srcRepo = ge.Repo.FullName
		l.Debug("Ignoring unhandled event type. (Might still be handled by external plugins.)")
	}
	// Demux events only to external plugins that require this event.
	if external := s.needDemux(eventType, srcRepo); len(external) > 0 {
		s.wg.Add(1)
		go s.demuxExternal(l, external, payload, h)
	}
	return nil
}

// needDemux returns whether there are any external plugins that need to
// get the present event.
func (s *Server) needDemux(eventType, orgRepo string) []plugins.ExternalPlugin {
	var matching []plugins.ExternalPlugin
	split := strings.Split(orgRepo, "/")
	srcOrg := split[0]
	var srcRepo string
	if len(split) > 1 {
		srcRepo = split[1]
	}
	if !s.RepoEnabled(srcOrg, srcRepo) {
		return nil
	}

	for repo, plugins := range s.Plugins.Config().ExternalPlugins {
		// Make sure the repositories match
		if repo != orgRepo && repo != srcOrg {
			continue
		}

		// Make sure the events match
		for _, p := range plugins {
			if len(p.Events) == 0 {
				matching = append(matching, p)
			} else {
				for _, et := range p.Events {
					if et != eventType {
						continue
					}
					matching = append(matching, p)
					break
				}
			}
		}
	}
	return matching
}

// demuxExternal dispatches the provided payload to the external plugins.
func (s *Server) demuxExternal(l *logrus.Entry, externalPlugins []plugins.ExternalPlugin, payload []byte, h http.Header) {
	defer s.wg.Done()
	h.Set("User-Agent", "ProwHook")
	for _, p := range externalPlugins {
		s.wg.Add(1)
		go func(p plugins.ExternalPlugin) {
			defer s.wg.Done()
			if err := s.dispatch(p.Endpoint, payload, h); err != nil {
				l.WithError(err).WithField("external-plugin", p.Name).Error("Error dispatching event to external plugin.")
			} else {
				l.WithField("external-plugin", p.Name).Info("Dispatched event to external plugin")
			}
		}(p)
	}
}

// dispatch creates a new request using the provided payload and headers
// and dispatches the request to the provided endpoint.
func (s *Server) dispatch(endpoint string, payload []byte, h http.Header) error {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header = h
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("response has status %q and body %q", resp.Status, string(rb))
	}
	return nil
}

// GracefulShutdown implements a graceful shutdown protocol. It handles all requests sent before
// receiving the shutdown signal.
func (s *Server) GracefulShutdown() {
	s.wg.Wait() // Handle remaining requests
}

func (s *Server) do(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	backoff := 100 * time.Millisecond
	maxRetries := 5

	for retries := 0; retries < maxRetries; retries++ {
		resp, err = s.c.Do(req)
		if err == nil {
			break
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return resp, err
}
