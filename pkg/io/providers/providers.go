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

package providers

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/memblob"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

const (
	S3 = "s3"
	GS = "gs"
	// TODO(danilo-gemoli): complete the implementation since at this time only opener.Writer()
	// is supported
	File = "file"
)

// DisplayName turns canonical provider IDs into displayable names.
func DisplayName(provider string) string {
	switch provider {
	case GS:
		return "GCS"
	case S3:
		return "S3"
	case File:
		return "File"
	}
	return provider
}

// GetBucket opens and returns a gocloud blob.Bucket based on credentials and a path.
// The path is used to discover which storageProvider should be used.
//
// If the storageProvider file is detected, we don't need any credentials and just open a file bucket
// If no credentials are given, we just fall back to blob.OpenBucket which tries to auto discover credentials
// e.g. via environment variables. For more details, see: https://gocloud.dev/howto/blob/
//
// If we specify credentials and an 3:// path is used, credentials must be given in one of the
// following formats:
//   - AWS S3 (s3://):
//     {
//     "region": "us-east-1",
//     "s3_force_path_style": true,
//     "access_key": "access_key",
//     "secret_key": "secret_key"
//     }
//   - S3-compatible service, e.g. self-hosted Minio (s3://):
//     {
//     "region": "minio",
//     "endpoint": "https://minio-hl-svc.minio-operator-ns:9000",
//     "s3_force_path_style": true,
//     "access_key": "access_key",
//     "secret_key": "secret_key"
//     }
func GetBucket(ctx context.Context, S3Credentials []byte, path string) (*blob.Bucket, error) {
	storageProvider, bucket, _, err := ParseStoragePath(path)
	if err != nil {
		return nil, err
	}
	if storageProvider == S3 && len(S3Credentials) > 0 {
		return getS3Bucket(ctx, S3Credentials, bucket)
	}

	bkt, err := blob.OpenBucket(ctx, fmt.Sprintf("%s://%s", storageProvider, bucket))
	if err != nil {
		return nil, fmt.Errorf("error opening file bucket: %w", err)
	}
	return bkt, nil
}

// S3Credentials are credentials used to access S3 or an S3-compatible storage service
// Endpoint is an optional property. Default is the AWS S3 endpoint. If set, the specified
// endpoint will be used instead.
type S3Credentials struct {
	Region           string `json:"region"`
	Endpoint         string `json:"endpoint"`
	Insecure         bool   `json:"insecure"`
	S3ForcePathStyle bool   `json:"s3_force_path_style"`
	AccessKey        string `json:"access_key"`
	SecretKey        string `json:"secret_key"`
	SessionToken     string `json:"session_token"`
}

// HasStorageProviderPrefix returns true if the given string starts with
// any of the known storageProviders and a slash, e.g.
// * gs/kubernetes-jenkins returns true
// * kubernetes-jenkins returns false
func HasStorageProviderPrefix(path string) bool {
	return strings.HasPrefix(path, GS+"/") || strings.HasPrefix(path, S3+"/")
}

// ParseStoragePath parses storagePath and returns the storageProvider, bucket and relativePath
// For example gs://prow-artifacts/test.log results in (gs, prow-artifacts, test.log)
// Currently detected storageProviders are GS, S3 and file.
// Paths with a leading / instead of a storageProvider prefix are treated as file paths for backwards
// compatibility reasons.
// File paths are split into a directory and a file. Directory is returned as bucket, file is returned.
// as relativePath.
// For all other paths the first part is treated as storageProvider prefix, the second segment as bucket
// and everything after the bucket as relativePath.
func ParseStoragePath(storagePath string) (storageProvider, bucket, relativePath string, err error) {
	parsedPath, err := url.Parse(storagePath)
	if err != nil {
		return "", "", "", fmt.Errorf("unable to parse path %q: %w", storagePath, err)
	}

	storageProvider = parsedPath.Scheme
	bucket, relativePath = parsedPath.Host, parsedPath.Path
	relativePath = strings.TrimPrefix(relativePath, "/")

	if bucket == "" {
		return "", "", "", fmt.Errorf("could not find bucket in storagePath %q", storagePath)
	}
	return storageProvider, bucket, relativePath, nil
}

// StoragePath is the reverse of ParseStoragePath.
func StoragePath(bucket, path string) (string, error) {
	pp, err := prowv1.ParsePath(bucket)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s/%s", pp.StorageProvider(), pp.Bucket(), path), nil
}
