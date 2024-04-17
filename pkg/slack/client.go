/*
Copyright 2017 The Kubernetes Authors.

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

package slack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
)

// HostsFlag is the flag type for slack hosts while initializing slack client
type HostsFlag map[string]string

func (h *HostsFlag) String() string {
	var hosts []string
	for host, tokenPath := range *h {
		hosts = append(hosts, host+"="+tokenPath)
	}
	return strings.Join(hosts, " ")
}

// Set populates ProjectsFlag upon flag.Parse()
func (h *HostsFlag) Set(value string) error {
	if len(*h) == 0 {
		*h = map[string]string{}
	}
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%s not in the form of host=token-path", value)
	}
	host, tokenPath := parts[0], parts[1]
	if _, ok := (*h)[host]; ok {
		return fmt.Errorf("duplicate host: %s", host)
	}
	(*h)[host] = tokenPath
	return nil
}

// Logger provides an interface to log debug messages.
type Logger interface {
	Debugf(s string, v ...interface{})
}

// Client allows you to provide connection to Slack API Server
// It contains a token that allows to authenticate connection to post and work with channels in the domain
type Client struct {
	// If logger is non-nil, log all method calls with it.
	logger Logger

	tokenGenerator func() []byte
	fake           bool
}

const (
	chatPostMessage = "https://slack.com/api/chat.postMessage"

	botName      = "prow"
	botIconEmoji = ":prow:"
)

// NewClient creates a slack client with an API token.
func NewClient(tokenGenerator func() []byte) *Client {
	return &Client{
		logger:         logrus.WithField("client", "slack"),
		tokenGenerator: tokenGenerator,
	}
}

// NewFakeClient returns a client that takes no actions.
func NewFakeClient() *Client {
	return &Client{
		fake: true,
	}
}

func (sl *Client) log(methodName string, args ...interface{}) {
	if sl.logger == nil {
		return
	}
	var as []string
	for _, arg := range args {
		as = append(as, fmt.Sprintf("%v", arg))
	}
	sl.logger.Debugf("%s(%s)", methodName, strings.Join(as, ", "))
}

func (sl *Client) urlValues() *url.Values {
	uv := url.Values{}
	uv.Add("username", botName)
	uv.Add("icon_emoji", botIconEmoji)
	uv.Add("token", string(sl.tokenGenerator()))
	return &uv
}

func (sl *Client) postMessage(url string, uv *url.Values) error {
	resp, err := http.PostForm(url, *uv)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	apiResponse := struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}{}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return fmt.Errorf("API returned invalid JSON (%q): %w", string(body), err)
	}

	if resp.StatusCode != 200 || !apiResponse.Ok {
		return fmt.Errorf("request failed: %s", apiResponse.Error)
	}

	return nil
}

// WriteMessage adds text to channel
func (sl *Client) WriteMessage(text, channel string) error {
	sl.log("WriteMessage", text, channel)
	if sl.fake {
		return nil
	}

	var uv = sl.urlValues()
	uv.Add("channel", channel)
	uv.Add("text", text)

	if err := sl.postMessage(chatPostMessage, uv); err != nil {
		return fmt.Errorf("failed to post message to %s: %w", channel, err)
	}
	return nil
}
