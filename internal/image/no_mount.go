package image

import (
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
)

// WithoutMountReuse wraps an image so remote.Write cannot treat its layers as
// mountable source blobs for cross-repository blob mounts. This matches the
// intent of skopeo's --dest-no-reuse fallback more closely than changing blob
// media type behavior.
func WithoutMountReuse(img v1.Image) v1.Image {
	return &noMountImage{Image: img}
}

type noMountImage struct {
	v1.Image
}

func (i *noMountImage) Layers() ([]v1.Layer, error) {
	layers, err := i.Image.Layers()
	if err != nil {
		return nil, err
	}
	return wrapLayers(layers), nil
}

func (i *noMountImage) LayerByDigest(h v1.Hash) (v1.Layer, error) {
	layer, err := i.Image.LayerByDigest(h)
	if err != nil {
		return nil, err
	}
	return wrapLayer(layer), nil
}

func (i *noMountImage) LayerByDiffID(h v1.Hash) (v1.Layer, error) {
	layer, err := i.Image.LayerByDiffID(h)
	if err != nil {
		return nil, err
	}
	return wrapLayer(layer), nil
}

func (i *noMountImage) ConfigLayer() (v1.Layer, error) {
	layer, err := partial.ConfigLayer(i.Image)
	if err != nil {
		return nil, err
	}
	return wrapLayer(layer), nil
}

func wrapLayers(layers []v1.Layer) []v1.Layer {
	wrapped := make([]v1.Layer, 0, len(layers))
	for _, layer := range layers {
		wrapped = append(wrapped, wrapLayer(layer))
	}
	return wrapped
}

func wrapLayer(layer v1.Layer) v1.Layer {
	if layer == nil {
		return nil
	}
	if _, ok := layer.(*noMountLayer); ok {
		return layer
	}
	return &noMountLayer{Layer: layer}
}

type noMountLayer struct {
	v1.Layer
}
