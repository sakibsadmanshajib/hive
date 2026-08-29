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
	err := signer.SignHTTP(ctx, credentials(accessKey, secretKey), req, payloadHash, "s3", region, signingTime)
	// Opaque exists only to hand the signer an un-normalized path, and it has
	// to be given back before the request goes on the wire.
	//
	// net/url.RequestURI returns Opaque verbatim, and a value starting with
	// "//" comes back with the scheme glued on, so a signed request was being
	// written as `PUT http://host/s3/bucket/key HTTP/1.1`. That is absolute
	// form, which RFC 9112 reserves for a request TO A PROXY; an origin server
	// is meant to receive `PUT /s3/bucket/key HTTP/1.1`. Most servers accept it
	// anyway, which is why this survived: on the deployed box every S3 request
	// goes through Caddy, which normalizes the target before proxying it on, so
	// Storage never sees the malformed form.
	//
	// Supabase Storage does not accept it. Its signature-v4 plugin builds
	// `new URL("http://localhost:8080" + prefix + request.url)`, so an absolute
	// target produces `http://localhost:8080http://host/s3/...`, and every
	// upload fails with a 500 ERR_INVALID_URL that names neither the path nor
	// the credential. That is what CI found the moment it was given a real
	// object store to talk to rather than a dead hostname (issue #1324).
	//
	// Clearing it after signing changes no signature. The signer derives its
	// canonical URI from Opaque while it is set (getURIPath strips the leading
	// "//host"), and that value is byte identical to the EscapedPath the wire
	// form falls back to once Opaque is empty. Cleared here rather than at the
	// call site because every S3 request in this package routes through this
	// function, and a caller that forgot would fail only against a server
	// strict enough to notice.
	//
	// presignHTTP deliberately does NOT do this: it needs Opaque to survive,
	// because url.URL.String renders a bare-path Opaque as `http:/s3/...` and
	// the "//host" form is what makes the presigned URL come out absolute.
	req.URL.Opaque = ""
	return err
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
