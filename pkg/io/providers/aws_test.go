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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/google/go-cmp/cmp"
)

func Test_newS3Client(t *testing.T) {
	// Prevent AWS SDK from loading config from ~/.aws/config or ~/.aws/credentials
	// which would set a default region from the user's environment
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")

	tests := []struct {
		name  string
		creds S3Credentials
		want  S3Credentials
	}{
		{
			name: "only accesskey and secretkey set",
			creds: S3Credentials{
				AccessKey: "foo",
				SecretKey: "bar",
			},
			want: S3Credentials{},
		},
		{
			name: "all options set ",
			creds: S3Credentials{
				AccessKey:        "foo",
				SecretKey:        "bar",
				Endpoint:         "https://foobar.com",
				Region:           "eu01",
				Insecure:         true,
				S3ForcePathStyle: true,
			},
			want: S3Credentials{
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
			got := S3Credentials{
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

func Test_newS3Client_RequestChecksumCalculation(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")

	tests := []struct {
		name     string
		creds    S3Credentials
		envValue string
		want     aws.RequestChecksumCalculation
		wantErr  bool
	}{
		{
			// Unset: the SDK default is left in place.
			name:  "unset keeps SDK default",
			creds: S3Credentials{AccessKey: "foo", SecretKey: "bar"},
			want:  aws.RequestChecksumCalculationWhenSupported,
		},
		{
			// Opt-in to when_required to avoid aws-chunked on a custom store.
			name:  "when_required is honored",
			creds: S3Credentials{AccessKey: "foo", SecretKey: "bar", Endpoint: "https://foobar.com", RequestChecksumCalculation: "when_required"},
			want:  aws.RequestChecksumCalculationWhenRequired,
		},
		{
			// Explicit when_supported is honored.
			name:  "when_supported is honored",
			creds: S3Credentials{AccessKey: "foo", SecretKey: "bar", Endpoint: "https://foobar.com", RequestChecksumCalculation: "when_supported"},
			want:  aws.RequestChecksumCalculationWhenSupported,
		},
		{
			// When the field is unset, the env var still takes effect.
			name:     "env var applies when field unset",
			creds:    S3Credentials{AccessKey: "foo", SecretKey: "bar", Endpoint: "https://foobar.com"},
			envValue: "when_required",
			want:     aws.RequestChecksumCalculationWhenRequired,
		},
		{
			// The explicit field wins over the env var.
			name:     "field overrides env var",
			creds:    S3Credentials{AccessKey: "foo", SecretKey: "bar", Endpoint: "https://foobar.com", RequestChecksumCalculation: "when_required"},
			envValue: "when_supported",
			want:     aws.RequestChecksumCalculationWhenRequired,
		},
		{
			// Matching is case-insensitive, like the SDK's env var parsing.
			name:  "value is case-insensitive",
			creds: S3Credentials{AccessKey: "foo", SecretKey: "bar", Endpoint: "https://foobar.com", RequestChecksumCalculation: "WHEN_REQUIRED"},
			want:  aws.RequestChecksumCalculationWhenRequired,
		},
		{
			// An unrecognized value is rejected.
			name:    "invalid value errors",
			creds:   S3Credentials{AccessKey: "foo", SecretKey: "bar", RequestChecksumCalculation: "nonsense"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// An empty env value is treated the same as the var being unset.
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", tt.envValue)
			client, err := newS3Client(context.Background(), &tt.creds)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("newS3Client() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("newS3Client() error = %v", err)
			}
			if got := client.Options().RequestChecksumCalculation; got != tt.want {
				t.Errorf("RequestChecksumCalculation = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_getS3Bucket_UploadChecksumHeaders drives a real write through the
// gocloud bucket and asserts on the headers actually sent to the store. This
// is what matters for S3-compatible stores that reject aws-chunked: the SDK
// client setting alone is not enough, because gocloud uploads through the
// transfer manager, which has its own checksum setting.
func Test_getS3Bucket_UploadChecksumHeaders(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "")

	tests := []struct {
		name                       string
		requestChecksumCalculation string
		wantAwsChunked             bool
	}{
		{
			// Default: the SDK streams with a trailing checksum, which is what
			// AWS S3 and chunked-capable stores expect.
			name:           "unset keeps aws-chunked",
			wantAwsChunked: true,
		},
		{
			name:                       "when_required disables aws-chunked",
			requestChecksumCalculation: "when_required",
			wantAwsChunked:             false,
		},
		{
			// Explicit when_supported keeps the trailing checksum, like the default.
			name:                       "when_supported keeps aws-chunked",
			requestChecksumCalculation: "when_supported",
			wantAwsChunked:             true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got http.Header
			// Trailing checksums are only used over HTTPS, so the server must be TLS.
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					got = r.Header.Clone()
				}
				w.Header().Set("ETag", `"etag"`)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			creds, err := json.Marshal(S3Credentials{
				AccessKey:                  "foo",
				SecretKey:                  "bar",
				Region:                     "us-east-1",
				Endpoint:                   srv.URL,
				Insecure:                   true,
				S3ForcePathStyle:           true,
				RequestChecksumCalculation: tt.requestChecksumCalculation,
			})
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			bkt, err := getS3Bucket(ctx, creds, "bucket")
			if err != nil {
				t.Fatalf("getS3Bucket() error = %v", err)
			}
			defer bkt.Close()

			w, err := bkt.NewWriter(ctx, "key", nil)
			if err != nil {
				t.Fatalf("NewWriter() error = %v", err)
			}
			if _, err := w.Write([]byte("hello")); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			if got == nil {
				t.Fatal("no PutObject request received")
			}

			gotAwsChunked := got.Get("Content-Encoding") == "aws-chunked"
			if gotAwsChunked != tt.wantAwsChunked {
				t.Errorf("Content-Encoding aws-chunked = %v, want %v (headers: Content-Encoding=%q X-Amz-Trailer=%q X-Amz-Content-Sha256=%q)",
					gotAwsChunked, tt.wantAwsChunked, got.Get("Content-Encoding"), got.Get("X-Amz-Trailer"), got.Get("X-Amz-Content-Sha256"))
			}
			if hasTrailer := got.Get("X-Amz-Trailer") != ""; hasTrailer != tt.wantAwsChunked {
				t.Errorf("X-Amz-Trailer present = %v, want %v", hasTrailer, tt.wantAwsChunked)
			}
		})
	}
}
