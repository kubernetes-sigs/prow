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

package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"sigs.k8s.io/prow/pkg/pluginhelp"
)

// cacheLife is the time that we keep a pluginhelp.Help struct before considering it stale.
// We consider help valid for a minute to prevent excessive calls to hook.
const cacheLife = time.Minute
const sslEnabledSchema = "https"

type helpAgent struct {
	path   string
	cert   string
	client *http.Client

	sync.Mutex
	help   *pluginhelp.Help
	expiry time.Time
}

func newHelpAgent(path string, cert string) (*helpAgent, error) {
	//Set custom http client if ssl is enabled
	var client *http.Client
	hookPath, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("error parsing hook path: %w", err)
	}
	if hookPath.Scheme == sslEnabledSchema {
		caCert, err := os.ReadFile(cert)
		if err != nil {
			return nil, fmt.Errorf("error decoding cert file: %w", err)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: caCertPool,
				},
			},
		}
	} else {
		client = http.DefaultClient
	}
	return &helpAgent{
		path:   path,
		cert:   cert,
		client: client,
	}, nil
}

// TODO: this function sets the plugin help for ts somehow
func (ha *helpAgent) getHelp() (*pluginhelp.Help, error) {
	ha.Lock()
	defer ha.Unlock()
	if time.Now().Before(ha.expiry) {
		return ha.help, nil
	}

	var help pluginhelp.Help
	resp, err := ha.client.Get(ha.path)
	if err != nil {
		return nil, fmt.Errorf("error Getting plugin help: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("response has status code %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&help); err != nil {
		return nil, fmt.Errorf("error decoding json plugin help: %w", err)
	}

	ha.help = &help
	ha.expiry = time.Now().Add(cacheLife)
	return &help, nil
}
