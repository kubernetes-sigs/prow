/*
Copyright 2020 The Kubernetes Authors.

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

package jira

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/andygrunwald/go-jira"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/prow/version"
)

type Client interface {
	GetIssue(id string) (*jira.Issue, error)
	GetRemoteLinks(id string) ([]jira.RemoteLink, error)
	AddRemoteLink(id string, link *jira.RemoteLink) error
	UpdateRemoteLink(id string, link *jira.RemoteLink) error
	ForPlugin(plugin string) Client
	ListProjects() (*jira.ProjectList, error)
	JiraClient() *jira.Client
	JiraURL() string
	Used() bool
	WithFields(fields logrus.Fields) Client
}

type BasicAuthGenerator func() (username, password string)
type BearerAuthGenerator func() (token string)

type Options struct {
	BasicAuth  BasicAuthGenerator
	BearerAuth BearerAuthGenerator
	LogFields  logrus.Fields
}

type Option func(*Options)

func WithBasicAuth(basicAuth BasicAuthGenerator) Option {
	return func(o *Options) {
		o.BasicAuth = basicAuth
	}
}

func WithBearerAuth(token BearerAuthGenerator) Option {
	return func(o *Options) {
		o.BearerAuth = token
	}
}

func WithFields(fields logrus.Fields) Option {
	return func(o *Options) {
		o.LogFields = fields
	}
}

func newJiraClient(endpoint string, o Options, retryingClient *retryablehttp.Client) (*jira.Client, error) {
	retryingClient.HTTPClient.Transport = &metricsTransport{
		upstream:       retryingClient.HTTPClient.Transport,
		pathSimplifier: pathSimplifier().Simplify,
		recorder:       requestResults,
	}
	retryingClient.HTTPClient.Transport = userAgentSettingTransport{
		userAgent: version.UserAgent(),
		upstream:  retryingClient.HTTPClient.Transport,
	}

	if o.BasicAuth != nil {
		retryingClient.HTTPClient.Transport = &basicAuthRoundtripper{
			generator: o.BasicAuth,
			upstream:  retryingClient.HTTPClient.Transport,
		}
	}

	if o.BearerAuth != nil {
		retryingClient.HTTPClient.Transport = &bearerAuthRoundtripper{
			generator: o.BearerAuth,
			upstream:  retryingClient.HTTPClient.Transport,
		}
	}

	return jira.NewClient(retryingClient.StandardClient(), endpoint)
}

func NewClient(endpoint string, opts ...Option) (Client, error) {
	o := Options{}
	for _, opt := range opts {
		opt(&o)
	}

	log := logrus.WithField("client", "jira")
	if len(o.LogFields) > 0 {
		log = log.WithFields(o.LogFields)
	}
	retryingClient := retryablehttp.NewClient()
	usedFlagTransport := &clientUsedTransport{
		m:        sync.Mutex{},
		upstream: retryingClient.HTTPClient.Transport,
	}
	retryingClient.HTTPClient.Transport = usedFlagTransport
	retryingClient.Logger = &retryableHTTPLogrusWrapper{log: log}

	jiraClient, err := newJiraClient(endpoint, o, retryingClient)
	if err != nil {
		return nil, err
	}
	url := jiraClient.GetBaseURL()
	return &client{delegate: &delegate{url: url.String(), options: o}, logger: log, upstream: jiraClient, clientUsed: usedFlagTransport}, err
}

type userAgentSettingTransport struct {
	userAgent string
	upstream  http.RoundTripper
}

func (u userAgentSettingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("User-Agent", u.userAgent)
	return u.upstream.RoundTrip(r)
}

type clientUsedTransport struct {
	used     bool
	m        sync.Mutex
	upstream http.RoundTripper
}

func (c *clientUsedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.m.Lock()
	c.used = true
	c.m.Unlock()
	return c.upstream.RoundTrip(r)
}

func (c *clientUsedTransport) Used() bool {
	c.m.Lock()
	defer c.m.Unlock()
	return c.used
}

type used interface {
	Used() bool
}

type client struct {
	logger     *logrus.Entry
	upstream   *jira.Client
	clientUsed used
	*delegate
}

// delegate actually does the work to talk to Jira
type delegate struct {
	url     string
	options Options
}

func (jc *client) JiraClient() *jira.Client {
	return jc.upstream
}

func (jc *client) GetIssue(id string) (*jira.Issue, error) {
	issue, response, err := jc.upstream.Issue.Get(id, &jira.GetQueryOptions{})
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil, NotFoundError{err}
		}
		return nil, JiraError(response, err)
	}

	return issue, nil
}

func (jc *client) ListProjects() (*jira.ProjectList, error) {
	projects, response, err := jc.upstream.Project.GetList()
	if err != nil {
		return nil, JiraError(response, err)
	}
	return projects, nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, NotFoundError{})
}

func NewNotFoundError(err error) error {
	return NotFoundError{err}
}

type NotFoundError struct {
	error
}

func (NotFoundError) Is(target error) bool {
	_, match := target.(NotFoundError)
	return match
}

func (jc *client) GetRemoteLinks(id string) ([]jira.RemoteLink, error) {
	result, resp, err := jc.upstream.Issue.GetRemoteLinks(id)
	if err != nil {
		return nil, JiraError(resp, err)
	}
	return *result, nil
}

func (jc *client) AddRemoteLink(id string, link *jira.RemoteLink) error {
	req, err := jc.upstream.NewRequest("POST", "rest/api/2/issue/"+id+"/remotelink", link)
	if err != nil {
		return fmt.Errorf("failed to construct request: %w", err)
	}
	resp, err := jc.upstream.Do(req, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to add link: %w", JiraError(resp, err))
	}

	return nil
}

func (jc *client) UpdateRemoteLink(id string, link *jira.RemoteLink) error {
	internalLinkId := fmt.Sprint(link.ID)
	req, err := jc.upstream.NewRequest("PUT", "rest/api/2/issue/"+id+"/remotelink/"+internalLinkId, link)
	if err != nil {
		return fmt.Errorf("failed to construct request: %w", err)
	}
	resp, err := jc.upstream.Do(req, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to update link: %w", JiraError(resp, err))
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to update link: expected status code %d but got %d instead", http.StatusNoContent, resp.StatusCode)
	}
	return nil
}

func (jc *client) JiraURL() string {
	return jc.url
}

// Used determines whether the client has been used
func (jc *client) Used() bool {
	return jc.clientUsed.Used()
}

type bearerAuthRoundtripper struct {
	generator BearerAuthGenerator
	upstream  http.RoundTripper
}

// WithFields clones the client, keeping the underlying delegate the same but adding
// fields to the logging context
func (jc *client) WithFields(fields logrus.Fields) Client {
	return &client{
		clientUsed: jc.clientUsed,
		upstream:   jc.upstream,
		logger:     jc.logger.WithFields(fields),
		delegate:   jc.delegate,
	}
}

// ForPlugin clones the client, keeping the underlying delegate the same but adding
// a plugin identifier and log field
func (jc *client) ForPlugin(plugin string) Client {
	pluginLogger := jc.logger.WithField("plugin", plugin)
	retryingClient := retryablehttp.NewClient()
	usedFlagTransport := &clientUsedTransport{
		m:        sync.Mutex{},
		upstream: retryingClient.HTTPClient.Transport,
	}
	retryingClient.HTTPClient.Transport = usedFlagTransport
	retryingClient.Logger = &retryableHTTPLogrusWrapper{log: pluginLogger}
	// ignore error as url.String() was passed to the delegate
	jiraClient, err := newJiraClient(jc.url, jc.options, retryingClient)
	if err != nil {
		pluginLogger.WithError(err).Error("invalid Jira URL")
		jiraClient = jc.upstream
	}
	return &client{
		logger:     pluginLogger,
		clientUsed: usedFlagTransport,
		upstream:   jiraClient,
		delegate:   jc.delegate,
	}
}

func (bart *bearerAuthRoundtripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := new(http.Request)
	*req2 = *req
	req2.URL = new(url.URL)
	*req2.URL = *req.URL
	token := bart.generator()
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	logrus.WithField("curl", toCurl(req2)).Trace("Executing http request")
	return bart.upstream.RoundTrip(req2)
}

type basicAuthRoundtripper struct {
	generator BasicAuthGenerator
	upstream  http.RoundTripper
}

func (bart *basicAuthRoundtripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := new(http.Request)
	*req2 = *req
	req2.URL = new(url.URL)
	*req2.URL = *req.URL
	user, pass := bart.generator()
	req2.SetBasicAuth(user, pass)
	logrus.WithField("curl", toCurl(req2)).Trace("Executing http request")
	return bart.upstream.RoundTrip(req2)
}

var knownAuthTypes = sets.NewString("bearer", "basic", "negotiate")

// maskAuthorizationHeader masks credential content from authorization headers
// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Authorization
func maskAuthorizationHeader(key string, value string) string {
	if !strings.EqualFold(key, "Authorization") {
		return value
	}
	if len(value) == 0 {
		return ""
	}
	var authType string
	if i := strings.Index(value, " "); i > 0 {
		authType = value[0:i]
	} else {
		authType = value
	}
	if !knownAuthTypes.Has(strings.ToLower(authType)) {
		return "<masked>"
	}
	if len(value) > len(authType)+1 {
		value = authType + " <masked>"
	} else {
		value = authType
	}
	return value
}

// JiraError collapses cryptic Jira errors to include response
// bodies if it's detected that the original error holds no
// useful context in the first place
func JiraError(response *jira.Response, err error) error {
	if err != nil && strings.Contains(err.Error(), "Please analyze the request body for more details.") {
		if response != nil && response.Response != nil {
			body, readError := ioutil.ReadAll(response.Body)
			if readError != nil && readError.Error() != "http: read on closed response body" {
				logrus.WithError(readError).Warn("Failed to read Jira response body.")
			}
			return fmt.Errorf("%w: %s", err, string(body))
		}
	}
	return err
}

// toCurl is a slightly adjusted copy of https://github.com/kubernetes/kubernetes/blob/74053d555d71a14e3853b97e204d7d6415521375/staging/src/k8s.io/client-go/transport/round_trippers.go#L339
func toCurl(r *http.Request) string {
	headers := ""
	for key, values := range r.Header {
		for _, value := range values {
			headers += fmt.Sprintf(` -H %q`, fmt.Sprintf("%s: %s", key, maskAuthorizationHeader(key, value)))
		}
	}

	return fmt.Sprintf("curl -k -v -X%s %s '%s'", r.Method, headers, r.URL.String())
}

type retryableHTTPLogrusWrapper struct {
	log *logrus.Entry
}

// fieldsForContext translates a list of context fields to a
// logrus format; any items that don't conform to our expectations
// are omitted
func (l *retryableHTTPLogrusWrapper) fieldsForContext(context ...interface{}) logrus.Fields {
	fields := logrus.Fields{}
	for i := 0; i < len(context)-1; i += 2 {
		key, ok := context[i].(string)
		if !ok {
			continue
		}
		fields[key] = context[i+1]
	}
	return fields
}

func (l *retryableHTTPLogrusWrapper) Error(msg string, context ...interface{}) {
	l.log.WithFields(l.fieldsForContext(context...)).Error(msg)
}

func (l *retryableHTTPLogrusWrapper) Info(msg string, context ...interface{}) {
	l.log.WithFields(l.fieldsForContext(context...)).Info(msg)
}

func (l *retryableHTTPLogrusWrapper) Debug(msg string, context ...interface{}) {
	l.log.WithFields(l.fieldsForContext(context...)).Debug(msg)
}

func (l *retryableHTTPLogrusWrapper) Warn(msg string, context ...interface{}) {
	l.log.WithFields(l.fieldsForContext(context...)).Warn(msg)
}
