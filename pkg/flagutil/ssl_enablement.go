/*
Copyright 2026 The Kubernetes Authors.

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

package flagutil

import (
	"errors"
	"flag"
)

// SSLEnablementOptions provides flags for enabling running deck and
// hook with SSL enabled. This will allow for the Prow Ingress to run with
// HTTPS backend. If enableSSL is true, both certFile and keyFile must be set
// to the location of the cert and key files respectively.

type SSLEnablementOptions struct {
	EnableSSL      bool
	ServerCertFile string
	ServerKeyFile  string
}

func (o *SSLEnablementOptions) AddFlags(fs *flag.FlagSet) {
	fs.BoolVar(&o.EnableSSL, "enable-ssl", false, "Enable SSL to support Ingress backend HTTPS")
	fs.StringVar(&o.ServerCertFile, "server-cert-file", "", "Location of the server cert file for hosting TLS call. This must be set if SSL is enabled.")
	fs.StringVar(&o.ServerKeyFile, "server-key-file", "", "Location of the key file for hosting TLS call. This must be set if SSL is enabled.")
}

func (o *SSLEnablementOptions) Validate(_ bool) error {
	if o.EnableSSL {
		if o.ServerCertFile == "" {
			return errors.New("flag --enable-ssl was set to true but required flag --cert-file was not set")
		}
		if o.ServerKeyFile == "" {
			return errors.New("flag --enable-ssl was set to true but required flag --key-file was not set")
		}
	}
	return nil
}
