// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package s3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/efficientgo/tools/core/pkg/testutil"
	"github.com/go-kit/log"
	"github.com/minio/minio-go/v7/pkg/encrypt"

	"github.com/thanos-io/objstore/exthttp"
)

const endpoint string = "localhost:80"

func TestParseConfig(t *testing.T) {
	input := []byte(`bucket: abcd
insecure: false`)
	cfg, err := parseConfig(input)
	testutil.Ok(t, err)

	if cfg.Bucket != "abcd" {
		t.Errorf("parsing of bucket failed: got %v, expected %v", cfg.Bucket, "abcd")
	}
	if cfg.Insecure {
		t.Errorf("parsing of insecure failed: got %v, expected %v", cfg.Insecure, false)
	}
}

func TestParseConfig_SSEConfig(t *testing.T) {
	input := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-S3`)

	cfg, err := parseConfig(input)
	testutil.Ok(t, err)
	testutil.Ok(t, validate(cfg))

	input2 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-C`)

	cfg, err = parseConfig(input2)
	testutil.Ok(t, err)
	testutil.NotOk(t, validate(cfg))

	input3 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-C
  kms_key_id: qweasd`)

	cfg, err = parseConfig(input3)
	testutil.Ok(t, err)
	testutil.NotOk(t, validate(cfg))

	input4 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-C
  encryption_key: /some/file`)

	cfg, err = parseConfig(input4)
	testutil.Ok(t, err)
	testutil.Ok(t, validate(cfg))

	input5 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-KMS`)

	cfg, err = parseConfig(input5)
	testutil.Ok(t, err)
	testutil.NotOk(t, validate(cfg))

	input6 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-KMS
  kms_key_id: abcd1234-ab12-cd34-1234567890ab`)

	cfg, err = parseConfig(input6)
	testutil.Ok(t, err)
	testutil.Ok(t, validate(cfg))

	input7 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-KMS
  kms_key_id: abcd1234-ab12-cd34-1234567890ab
  kms_encryption_context:
    key: value
    something: else
    a: b`)

	cfg, err = parseConfig(input7)
	testutil.Ok(t, err)
	testutil.Ok(t, validate(cfg))

	input8 := []byte(`bucket: abdd
endpoint: "s3-endpoint"
sse_config:
  type: SSE-MagicKey
  kms_key_id: abcd1234-ab12-cd34-1234567890ab
  encryption_key: /some/file`)

	cfg, err = parseConfig(input8)
	testutil.Ok(t, err)
	// Since the error handling for "proper type" if done as we're setting up the bucket.
	testutil.Ok(t, validate(cfg))
}

func TestParseConfig_DefaultHTTPConfig(t *testing.T) {
	input := []byte(`bucket: abcd
insecure: false`)
	cfg, err := parseConfig(input)
	testutil.Ok(t, err)

	if time.Duration(cfg.HTTPConfig.IdleConnTimeout) != time.Duration(90*time.Second) {
		t.Errorf("parsing of idle_conn_timeout failed: got %v, expected %v",
			time.Duration(cfg.HTTPConfig.IdleConnTimeout), time.Duration(90*time.Second))
	}

	if time.Duration(cfg.HTTPConfig.ResponseHeaderTimeout) != time.Duration(2*time.Minute) {
		t.Errorf("parsing of response_header_timeout failed: got %v, expected %v",
			time.Duration(cfg.HTTPConfig.IdleConnTimeout), time.Duration(2*time.Minute))
	}

	if cfg.HTTPConfig.InsecureSkipVerify {
		t.Errorf("parsing of insecure_skip_verify failed: got %v, expected %v", cfg.HTTPConfig.InsecureSkipVerify, false)
	}
}

func TestParseConfig_CustomHTTPConfig(t *testing.T) {
	input := []byte(`bucket: abcd
insecure: false
http_config:
  insecure_skip_verify: true
  idle_conn_timeout: 50s
  response_header_timeout: 1m`)
	cfg, err := parseConfig(input)
	testutil.Ok(t, err)

	if time.Duration(cfg.HTTPConfig.IdleConnTimeout) != time.Duration(50*time.Second) {
		t.Errorf("parsing of idle_conn_timeout failed: got %v, expected %v",
			time.Duration(cfg.HTTPConfig.IdleConnTimeout), time.Duration(50*time.Second))
	}

	if time.Duration(cfg.HTTPConfig.ResponseHeaderTimeout) != time.Duration(1*time.Minute) {
		t.Errorf("parsing of response_header_timeout failed: got %v, expected %v",
			time.Duration(cfg.HTTPConfig.IdleConnTimeout), time.Duration(1*time.Minute))
	}

	if !cfg.HTTPConfig.InsecureSkipVerify {
		t.Errorf("parsing of insecure_skip_verify failed: got %v, expected %v", cfg.HTTPConfig.InsecureSkipVerify, false)
	}
}

func TestParseConfig_CustomHTTPConfigWithTLS(t *testing.T) {
	input := []byte(`bucket: abcd
insecure: false
http_config:
  tls_config:
    ca_file: /certs/ca.crt
    cert_file: /certs/cert.crt
    key_file: /certs/key.key
    server_name: server
    insecure_skip_verify: false
  `)
	cfg, err := parseConfig(input)
	testutil.Ok(t, err)

	testutil.Equals(t, "/certs/ca.crt", cfg.HTTPConfig.TLSConfig.CAFile)
	testutil.Equals(t, "/certs/cert.crt", cfg.HTTPConfig.TLSConfig.CertFile)
	testutil.Equals(t, "/certs/key.key", cfg.HTTPConfig.TLSConfig.KeyFile)
	testutil.Equals(t, "server", cfg.HTTPConfig.TLSConfig.ServerName)
	testutil.Equals(t, false, cfg.HTTPConfig.TLSConfig.InsecureSkipVerify)
}

func TestParseConfig_CustomLegacyInsecureSkipVerify(t *testing.T) {
	input := []byte(`bucket: abcd
insecure: false
http_config:
  insecure_skip_verify: true
  tls_config:
    insecure_skip_verify: false
  `)
	cfg, err := parseConfig(input)
	testutil.Ok(t, err)
	transport, err := exthttp.DefaultTransport(cfg.HTTPConfig)
	testutil.Ok(t, err)
	testutil.Equals(t, true, transport.TLSClientConfig.InsecureSkipVerify)
}

func TestValidate_OK(t *testing.T) {
	input := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"
access_key: "access_key"
insecure: false
signature_version2: false
secret_key: "secret_key"
http_config:
  insecure_skip_verify: false
  idle_conn_timeout: 50s`)
	cfg, err := parseConfig(input)
	testutil.Ok(t, err)
	testutil.Ok(t, validate(cfg))
	testutil.Assert(t, cfg.PutUserMetadata != nil, "map should not be nil")

	input2 := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"
access_key: "access_key"
insecure: false
signature_version2: false
secret_key: "secret_key"
put_user_metadata:
  "X-Amz-Acl": "bucket-owner-full-control"
http_config:
  idle_conn_timeout: 0s`)
	cfg2, err := parseConfig(input2)
	testutil.Ok(t, err)
	testutil.Ok(t, validate(cfg2))

	testutil.Equals(t, "bucket-owner-full-control", cfg2.PutUserMetadata["X-Amz-Acl"])
}

func TestParseConfig_PartSize(t *testing.T) {
	input := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"
access_key: "access_key"
insecure: false
signature_version2: false
secret_key: "secret_key"
http_config:
  insecure_skip_verify: false
  idle_conn_timeout: 50s`)

	cfg, err := parseConfig(input)
	testutil.Ok(t, err)
	testutil.Assert(t, cfg.PartSize == 1024*1024*64, "when part size not set it should default to 128MiB")

	input2 := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"
access_key: "access_key"
insecure: false
signature_version2: false
secret_key: "secret_key"
part_size: 104857600
http_config:
  insecure_skip_verify: false
  idle_conn_timeout: 50s`)
	cfg2, err := parseConfig(input2)
	testutil.Ok(t, err)
	testutil.Assert(t, cfg2.PartSize == 1024*1024*100, "when part size should be set to 100MiB")
}

func TestParseConfig_OldSEEncryptionFieldShouldFail(t *testing.T) {
	input := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"
access_key: "access_key"
insecure: false
signature_version2: false
encrypt_sse: false
secret_key: "secret_key"
see_encryption: true
put_user_metadata:
  "X-Amz-Acl": "bucket-owner-full-control"
http_config:
  idle_conn_timeout: 0s`)
	_, err := parseConfig(input)
	testutil.NotOk(t, err)
}

func TestParseConfig_ListObjectsV1(t *testing.T) {
	input := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"`)

	cfg, err := parseConfig(input)
	testutil.Ok(t, err)

	if cfg.ListObjectsVersion != "" {
		t.Errorf("when list_objects_version not set, it should default to empty")
	}

	input2 := []byte(`bucket: "bucket-name"
endpoint: "s3-endpoint"
list_objects_version: "abcd"`)

	cfg2, err := parseConfig(input2)
	testutil.Ok(t, err)

	if cfg2.ListObjectsVersion != "abcd" {
		t.Errorf("parsing of list_objects_version failed: got %v, expected %v", cfg.ListObjectsVersion, "abcd")
	}
}

func TestBucket_getServerSideEncryption(t *testing.T) {
	// Default config should return no SSE config.
	cfg := DefaultConfig
	cfg.Endpoint = endpoint
	bkt, err := NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)

	sse, err := bkt.getServerSideEncryption(context.Background())
	testutil.Ok(t, err)
	testutil.Equals(t, nil, sse)

	// If SSE is configured in the client config it should be used.
	cfg = DefaultConfig
	cfg.Endpoint = endpoint
	cfg.SSEConfig = SSEConfig{Type: SSES3}
	bkt, err = NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)

	sse, err = bkt.getServerSideEncryption(context.Background())
	testutil.Ok(t, err)
	testutil.Equals(t, encrypt.S3, sse.Type())

	// SSE-KMS can be configured in the client config with an optional
	// KMSEncryptionContext - In this case the encryptionContextHeader should be
	// a base64 encoded string which represents a string-string map "{}"
	cfg = DefaultConfig
	cfg.Endpoint = endpoint
	cfg.SSEConfig = SSEConfig{
		Type:     SSEKMS,
		KMSKeyID: "key",
	}
	bkt, err = NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)

	sse, err = bkt.getServerSideEncryption(context.Background())
	testutil.Ok(t, err)
	testutil.Equals(t, encrypt.KMS, sse.Type())

	encryptionContextHeader := "X-Amz-Server-Side-Encryption-Context"
	headers := make(http.Header)
	sse.Marshal(headers)
	wantJson, err := json.Marshal(make(map[string]string))
	testutil.Ok(t, err)
	want := base64.StdEncoding.EncodeToString(wantJson)
	testutil.Equals(t, want, headers.Get(encryptionContextHeader))

	// If the KMSEncryptionContext is set then the header should reflect it's
	// value.
	cfg = DefaultConfig
	cfg.Endpoint = endpoint
	cfg.SSEConfig = SSEConfig{
		Type:                 SSEKMS,
		KMSKeyID:             "key",
		KMSEncryptionContext: map[string]string{"foo": "bar"},
	}
	bkt, err = NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)

	sse, err = bkt.getServerSideEncryption(context.Background())
	testutil.Ok(t, err)
	testutil.Equals(t, encrypt.KMS, sse.Type())

	headers = make(http.Header)
	sse.Marshal(headers)
	wantJson, err = json.Marshal(cfg.SSEConfig.KMSEncryptionContext)
	testutil.Ok(t, err)
	want = base64.StdEncoding.EncodeToString(wantJson)
	testutil.Equals(t, want, headers.Get(encryptionContextHeader))

	// If SSE is configured in the context it should win.
	cfg = DefaultConfig
	cfg.Endpoint = endpoint
	cfg.SSEConfig = SSEConfig{Type: SSES3}
	override, err := encrypt.NewSSEKMS("test", nil)
	testutil.Ok(t, err)

	bkt, err = NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)

	sse, err = bkt.getServerSideEncryption(context.WithValue(context.Background(), sseConfigKey, override))
	testutil.Ok(t, err)
	testutil.Equals(t, encrypt.KMS, sse.Type())
}

func TestBucket_Get_ShouldReturnErrorIfServerTruncateResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Length", "100")

		// Write less bytes than the content length.
		_, err := w.Write([]byte("12345"))
		testutil.Ok(t, err)
	}))
	defer srv.Close()

	cfg := DefaultConfig
	cfg.Bucket = "test-bucket"
	cfg.Endpoint = srv.Listener.Addr().String()
	cfg.Insecure = true
	cfg.Region = "test"
	cfg.AccessKey = "test"
	cfg.SecretKey = "test"

	bkt, err := NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)

	reader, err := bkt.Get(context.Background(), "test")
	testutil.Ok(t, err)

	// We expect an error when reading back.
	_, err = io.ReadAll(reader)
	testutil.Equals(t, io.ErrUnexpectedEOF, err)
}

func TestParseConfig_CustomStorageClass(t *testing.T) {
	for _, testCase := range []struct {
		name, storageClassKey string
	}{
		{name: "ProperCase", storageClassKey: "X-Amz-Storage-Class"},
		{name: "UpperCase", storageClassKey: "X-AMZ-STORAGE-CLASS"},
		{name: "LowerCase", storageClassKey: "x-amz-storage-class"},
		{name: "MixedCase", storageClassKey: "X-Amz-sToraGe-Class"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := DefaultConfig
			cfg.Endpoint = endpoint
			storageClass := "STANDARD_IA"
			cfg.PutUserMetadata[testCase.storageClassKey] = storageClass
			bkt, err := NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
			testutil.Ok(t, err)
			testutil.Equals(t, storageClass, bkt.storageClass)
		})
	}
}

func TestParseConfig_DefaultStorageClassIsZero(t *testing.T) {
	cfg := DefaultConfig
	cfg.Endpoint = endpoint
	bkt, err := NewBucketWithConfig(log.NewNopLogger(), cfg, "test")
	testutil.Ok(t, err)
	testutil.Equals(t, "", bkt.storageClass)
}
