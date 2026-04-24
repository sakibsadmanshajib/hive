package storage

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const unsignedPayload = "UNSIGNED-PAYLOAD"

func SignHTTP(ctx context.Context, signer *v4.Signer, req *http.Request, accessKey, secretKey, region string, signingTime time.Time, payloadHash string) error {
	if payloadHash == "" {
		payloadHash = unsignedPayload
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)
	setOpaquePath(req)
	return signer.SignHTTP(ctx, credentials(accessKey, secretKey), req, payloadHash, "s3", region, signingTime)
}

func presignHTTP(ctx context.Context, signer *v4.Signer, req *http.Request, accessKey, secretKey, region string, signingTime time.Time, ttl time.Duration) (string, error) {
	req.Header.Set("x-amz-content-sha256", unsignedPayload)
	query := req.URL.Query()
	query.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	req.URL.RawQuery = query.Encode()
	setOpaquePath(req)
	signedURL, _, err := signer.PresignHTTP(ctx, credentials(accessKey, secretKey), req, unsignedPayload, "s3", region, signingTime)
	return signedURL, err
}

func credentials(accessKey, secretKey string) aws.Credentials {
	return aws.Credentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	}
}

func setOpaquePath(req *http.Request) {
	req.URL.Opaque = "//" + req.URL.Host + req.URL.EscapedPath()
}
