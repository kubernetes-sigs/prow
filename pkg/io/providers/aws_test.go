/*
Copyright 2024 The Kubernetes Authors.

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

package providers

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/google/go-cmp/cmp"
)

func Test_newS3Client(t *testing.T) {
	tests := []struct {
		name  string
		creds s3Credentials
		want  s3Credentials
	}{
		{
			name: "only accesskey and secretkey set",
			creds: s3Credentials{
				AccessKey: "foo",
				SecretKey: "bar",
			},
			want: s3Credentials{},
		},
		{
			name: "all options set ",
			creds: s3Credentials{
				AccessKey:        "foo",
				SecretKey:        "bar",
				Endpoint:         "https://foobar.com",
				Region:           "eu01",
				Insecure:         true,
				S3ForcePathStyle: true,
			},
			want: s3Credentials{
				Endpoint:         "https://foobar.com",
				Region:           "eu01",
				Insecure:         true,
				S3ForcePathStyle: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client, err := newS3Client(ctx, &tt.creds)
			if err != nil {
				t.Errorf("newS3Client() error = %v,", err)
				return
			}
			s3opts := client.Options()
			if tt.creds.AccessKey != "" && tt.creds.SecretKey != "" {
				cache, ok := s3opts.Credentials.(*aws.CredentialsCache)
				if !ok {
					t.Errorf("credentials should be Cache, got %T,", s3opts.Credentials)
					return
				}
				if !cache.IsCredentialsProvider(credentials.StaticCredentialsProvider{}) {
					t.Errorf("credentialsprovider is not StaticCredentialsProvider")
				}
			}

			httpClient, ok := s3opts.HTTPClient.(*awshttp.BuildableClient)
			if !ok {
				t.Errorf("s3 HTTPClient is not a awshttp.BuildableClient, got %T", s3opts.HTTPClient)
				return
			}
			tlsConfig := httpClient.GetTransport().TLSClientConfig
			if tlsConfig == nil {
				t.Error("tlsConfig of s3 httpClient transport is nil")
				return
			}
			var endpoint string
			if s3opts.BaseEndpoint == nil {
				endpoint = ""
			} else {
				endpoint = *s3opts.BaseEndpoint
			}
			got := s3Credentials{
				Region:           s3opts.Region,
				Endpoint:         endpoint,
				S3ForcePathStyle: s3opts.UsePathStyle,
				Insecure:         tlsConfig.InsecureSkipVerify,
			}

			filterSecretsCmpOption := cmp.FilterPath(func(p cmp.Path) bool {
				if p.String() == "AccessKey" || p.String() == "SecretKey" {
					return true
				}
				return false
			}, cmp.Ignore())

			if diff := cmp.Diff(got, tt.want, filterSecretsCmpOption); diff != "" {
				t.Errorf("newS3Client() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
