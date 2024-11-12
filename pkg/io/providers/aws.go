package providers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"gocloud.dev/blob"
	"gocloud.dev/blob/s3blob"
)

const S3MinUploadPartSize = s3manager.MinUploadPartSize

// getS3Bucket opens a gocloud blob.Bucket based on given credentials in the format the
// struct s3Credentials defines (see documentation of GetBucket for an example)
func getS3Bucket(ctx context.Context, creds []byte, bucketName string) (*blob.Bucket, error) {
	s3Creds := &s3Credentials{}
	if err := json.Unmarshal(creds, s3Creds); err != nil {
		return nil, fmt.Errorf("error getting S3 credentials from JSON: %w", err)
	}

	cfg := &aws.Config{}

	//  Use the default credential chain if no credentials are specified
	if s3Creds.AccessKey != "" && s3Creds.SecretKey != "" {
		staticCredentials := credentials.StaticProvider{
			Value: credentials.Value{
				AccessKeyID:     s3Creds.AccessKey,
				SecretAccessKey: s3Creds.SecretKey,
			},
		}

		cfg.Credentials = credentials.NewChainCredentials([]credentials.Provider{&staticCredentials})
	}

	cfg.Endpoint = aws.String(s3Creds.Endpoint)
	cfg.DisableSSL = aws.Bool(s3Creds.Insecure)
	cfg.S3ForcePathStyle = aws.Bool(s3Creds.S3ForcePathStyle)
	cfg.Region = aws.String(s3Creds.Region)

	sess, err := session.NewSession(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating S3 Session: %w", err)
	}

	bkt, err := s3blob.OpenBucket(ctx, sess, bucketName, nil)
	if err != nil {
		return nil, fmt.Errorf("error opening S3 bucket: %w", err)
	}
	return bkt, nil
}
