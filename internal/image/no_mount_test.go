package image

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/datalite/oci-patch/internal/testutil"
)

func TestWithoutMountReuseStripsMountableLayers(t *testing.T) {
	server := httptest.NewServer(registry.New())
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", u.Host, "src/app", "latest"), name.Insecure)
	if err != nil {
		t.Fatal(err)
	}

	img := testutil.NewImageWithFiles(t, map[string][]byte{"app.txt": []byte("hello")})
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("write source image: %v", err)
	}

	pulled, err := remote.Image(ref)
	if err != nil {
		t.Fatalf("pull source image: %v", err)
	}

	layers, err := pulled.Layers()
	if err != nil {
		t.Fatalf("read pulled layers: %v", err)
	}
	if len(layers) == 0 {
		t.Fatal("expected pulled image to have at least one layer")
	}
	if _, ok := layers[0].(*remote.MountableLayer); !ok {
		t.Fatalf("expected pulled layer to be *remote.MountableLayer, got %T", layers[0])
	}

	configLayer, err := partial.ConfigLayer(pulled)
	if err != nil {
		t.Fatalf("read pulled config layer: %v", err)
	}
	if _, ok := configLayer.(*remote.MountableLayer); !ok {
		t.Fatalf("expected pulled config layer to be *remote.MountableLayer, got %T", configLayer)
	}

	wrapped := WithoutMountReuse(pulled)
	wrappedLayers, err := wrapped.Layers()
	if err != nil {
		t.Fatalf("read wrapped layers: %v", err)
	}
	if _, ok := wrappedLayers[0].(*remote.MountableLayer); ok {
		t.Fatalf("expected wrapped layer to strip *remote.MountableLayer, got %T", wrappedLayers[0])
	}

	wrappedConfigLayer, err := partial.ConfigLayer(wrapped)
	if err != nil {
		t.Fatalf("read wrapped config layer: %v", err)
	}
	if _, ok := wrappedConfigLayer.(*remote.MountableLayer); ok {
		t.Fatalf("expected wrapped config layer to strip *remote.MountableLayer, got %T", wrappedConfigLayer)
	}
}
