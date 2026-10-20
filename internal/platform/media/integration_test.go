package media

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Pinned to deploy/compose/docker-compose.yml.
const (
	seaweedImage  = "chrislusf/seaweedfs:4.48"
	imgproxyImage = "darthsim/imgproxy:v3.28.0"
	accessKey     = "local-dev-access"
	secretKey     = "local-dev-secret-not-for-prod"
	bucket        = "iftah-media"
)

type stack struct {
	s3       *S3
	signer   *Signer
	s3URL    string
	imgproxy string
}

// startStack runs SeaweedFS (S3) and imgproxy on one Docker network,
// configured like deploy/compose.
func startStack(t *testing.T) stack {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	net, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = net.Remove(context.Background()) })

	_, file, _, _ := runtime.Caller(0)
	s3json := filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "compose", "seaweedfs", "s3.json")
	weed, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image: seaweedImage,
		Cmd: []string{"server", "-dir=/data", "-ip.bind=0.0.0.0", "-master.volumeSizeLimitMB=64", "-volume.max=0",
			"-s3", "-s3.port=8333", "-s3.config=/etc/seaweedfs/s3.json"},
		Files:          []testcontainers.ContainerFile{{HostFilePath: s3json, ContainerFilePath: "/etc/seaweedfs/s3.json", FileMode: 0o644}},
		ExposedPorts:   []string{"8333/tcp"},
		Networks:       []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"seaweedfs"}},
		WaitingFor:     wait.ForListeningPort("8333/tcp").WithStartupTimeout(2 * time.Minute),
	}})
	if weed != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(weed) })
	}
	if err != nil {
		t.Fatal(err)
	}
	s3Endpoint, err := weed.PortEndpoint(ctx, "8333/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}

	key, salt := []byte("integration-test-imgproxy-key-32"), []byte("integration-salt")
	proxy, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image: imgproxyImage,
		Env: map[string]string{
			"IMGPROXY_KEY": hex.EncodeToString(key), "IMGPROXY_SALT": hex.EncodeToString(salt),
			"IMGPROXY_USE_S3": "true", "IMGPROXY_S3_ENDPOINT": "http://seaweedfs:8333",
			"IMGPROXY_S3_ENDPOINT_USING_PATH_STYLE": "true", "IMGPROXY_S3_REGION": "us-east-1",
			"AWS_ACCESS_KEY_ID": accessKey, "AWS_SECRET_ACCESS_KEY": secretKey,
			"IMGPROXY_ALLOWED_SOURCES": "s3://" + bucket + "/",
		},
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		WaitingFor:   wait.ForHTTP("/health").WithPort("8080/tcp").WithStartupTimeout(2 * time.Minute),
	}})
	if proxy != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(proxy) })
	}
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := proxy.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}

	// Create the bucket (deploy/compose does it with seaweedfs-init).
	admin, err := minio.New(strings.TrimPrefix(s3Endpoint, "http://"), &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Region: "us-east-1", BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		err = admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second) // the S3 gateway answers before the volume server is ready
	}
	if err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	s3, err := NewS3(S3Config{Endpoint: s3Endpoint, PublicEndpoint: s3Endpoint, Region: "us-east-1", Bucket: bucket,
		AccessKey: accessKey, SecretKey: secretKey, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return stack{s3: s3, signer: NewSigner(proxyURL, bucket, key, salt), s3URL: s3Endpoint, imgproxy: proxyURL}
}

// browserUpload does what the dashboard does with a presigned upload and
// returns the status and body.
func browserUpload(t *testing.T, up Upload, data []byte) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range up.Fields {
		_ = mw.WriteField(k, v)
	}
	fw, err := mw.CreateFormFile("file", "photo")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, up.URL, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func get(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), b
}

// TestStorageAndDelivery runs the real path: presigned browser upload to
// S3, processing, master upload, and delivery through signed imgproxy
// URLs; tampered URLs and policy violations are refused.
func TestStorageAndDelivery(t *testing.T) {
	st := startStack(t)
	ctx := t.Context()
	const maxBytes = 256 << 10

	up, err := st.s3.PresignUpload(ctx, "stores/A/uploads/1", "image/jpeg", maxBytes, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	original := encJPEG(t, solid(1200, 600, color.NRGBA{R: 30, G: 120, B: 200, A: 255}))
	if code, body := browserUpload(t, up, original); code/100 != 2 {
		t.Fatalf("upload = %d %s", code, body)
	}
	got, err := st.s3.Get(ctx, "stores/A/uploads/1", maxBytes)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("get original: %v (%d bytes)", err, len(got))
	}

	// The policy is enforced by the storage service.
	big, err := st.s3.PresignUpload(ctx, "stores/A/uploads/2", "image/jpeg", 1024, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := browserUpload(t, big, original); code/100 == 2 {
		t.Fatalf("oversized upload accepted: %d", code)
	}
	if _, err = st.s3.Get(ctx, "stores/A/uploads/2", maxBytes); !errors.Is(err, ErrNoObject) {
		t.Fatalf("oversized upload stored: %v", err)
	}
	other := up
	other.Fields = map[string]string{}
	for k, v := range up.Fields {
		other.Fields[k] = v
	}
	other.Fields["key"] = "stores/B/uploads/1" // not the key the policy was signed for
	if code, _ := browserUpload(t, other, original); code/100 == 2 {
		t.Fatalf("upload to another key accepted: %d", code)
	}

	m, err := Process(bytes.NewReader(got), Limits{MaxBytes: maxBytes, MaxPixels: 4_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.s3.Put(ctx, "stores/A/images/1.jpg", m.ContentType, m.Data); err != nil {
		t.Fatal(err)
	}
	for _, f := range Formats() {
		code, ct, body := get(t, st.signer.URL("stores/A/images/1.jpg", 400, f))
		if code != http.StatusOK || ct != "image/"+f || len(body) == 0 {
			t.Fatalf("%s delivery = %d %q (%d bytes)", f, code, ct, len(body))
		}
	}
	urls := st.signer.URLs("stores/A/images/1.jpg")
	if code, _, _ := get(t, urls["thumb"]["webp"]); code != http.StatusOK {
		t.Fatalf("thumb = %d", code)
	}

	// Tampering with size or source breaks the signature.
	good := st.signer.URL("stores/A/images/1.jpg", 400, "webp")
	for _, bad := range []string{
		strings.Replace(good, "rs:fit:400:400", "rs:fit:4000:4000", 1),
		strings.Replace(good, "/rs:fit", "/x/rs:fit", 1),
		NewSigner(st.imgproxy, bucket, []byte("wrong-key-wrong-key-wrong-key-00"), []byte("integration-salt")).URL("stores/A/images/1.jpg", 400, "webp"),
	} {
		if code, _, _ := get(t, bad); code != http.StatusForbidden {
			t.Errorf("tampered URL = %d, want 403: %s", code, bad)
		}
	}

	for _, k := range []string{"stores/A/uploads/1", "stores/A/images/1.jpg", "stores/A/never-existed"} {
		if err = st.s3.Delete(ctx, k); err != nil {
			t.Fatalf("delete %s: %v", k, err)
		}
	}
	if _, err = st.s3.Get(ctx, "stores/A/images/1.jpg", maxBytes); !errors.Is(err, ErrNoObject) {
		t.Fatalf("deleted object: %v", err)
	}
}
