package kubeimages

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestListImagesIncludesContainersAndInitContainers(t *testing.T) {
	client := &Client{client: fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "example"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Image: "registry/init:1"}},
			Containers:     []corev1.Container{{Image: "registry/app:1"}, {Image: "registry/sidecar:1"}},
		}}},
	}).AppsV1()}
	images, err := client.ListImages(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 {
		t.Fatalf("images = %#v", images)
	}
}
