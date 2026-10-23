/*
Copyright The Kubernetes Authors.

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

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/cmd/external-plugins/geminiagent/plugin"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"
)

type genericCommentHandler func(plugin.GitHubClient, *plugins.Configuration, *logrus.Entry, github.GenericCommentEvent) error

// Server implements http.Handler. It validates incoming GitHub webhooks and
// dispatches issue comments to the Gemini agent.
type Server struct {
	tokenGenerator func() []byte
	ghc            plugin.GitHubClient
	log            *logrus.Entry
	pluginAgent    *plugins.ConfigAgent

	handleGenericComment genericCommentHandler
}

// ServeHTTP validates an incoming webhook and dispatches it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		return
	}
	fmt.Fprint(w, "Event received. Have a nice day.")

	if err := s.handleEvent(eventType, eventGUID, payload); err != nil {
		s.log.WithError(err).Error("Error parsing event.")
	}
}

func (s *Server) handleEvent(eventType, eventGUID string, payload []byte) error {
	l := s.log.WithFields(logrus.Fields{
		"event-type":     eventType,
		github.EventGUID: eventGUID,
	})
	switch eventType {
	case "issue_comment":
		var ice github.IssueCommentEvent
		if err := json.Unmarshal(payload, &ice); err != nil {
			return err
		}
		ice.GUID = eventGUID
		go func() {
			if err := s.handleIssueComment(l, ice); err != nil {
				l.WithError(err).Info("Gemini agent failed.")
			}
		}()
	default:
		l.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

func (s *Server) handleIssueComment(l *logrus.Entry, ice github.IssueCommentEvent) error {
	if s.handleGenericComment == nil {
		return errors.New("generic comment handler is nil")
	}

	event, err := github.GeneralizeComment(ice)
	if err != nil {
		return err
	}

	l = l.WithFields(logrus.Fields{
		github.OrgLogField:  event.Repo.Owner.Login,
		github.RepoLogField: event.Repo.Name,
		github.PrLogField:   event.Number,
		"commenter":         event.User.Login,
		"url":               event.HTMLURL,
	})
	return s.handleGenericComment(s.ghc, s.pluginAgent.Config(), l, *event)
}
