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

func TestListDeploymentImagesPreservesSelectorsAndRepeatedImages(t *testing.T) {
	const sharedImage = "registry.example.com/team/shared@sha256:0123456789abcdef"
	client := &Client{client: fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "example"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{{
					Name:  "migrate",
					Image: "registry.example.com/team/migrate:1",
				}},
				Containers: []corev1.Container{
					{Name: "worker", Image: sharedImage},
					{Name: "metrics", Image: "registry.example.com/team/metrics:1"},
				},
			}}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "example"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "api", Image: sharedImage}},
			}}},
		},
	).AppsV1()}

	images, err := client.ListDeploymentImages(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 {
		t.Fatalf("images = %#v", images)
	}
	want := []DeploymentImage{
		{AppName: "api", ContainerName: "api", Image: sharedImage},
		{AppName: "worker", ContainerName: "metrics", Image: "registry.example.com/team/metrics:1"},
		{AppName: "worker", ContainerName: "worker", Image: sharedImage},
	}
	for i := range want {
		if images[i] != want[i] {
			t.Errorf("images[%d] = %#v, want %#v", i, images[i], want[i])
		}
	}
}

func TestListDeploymentImagesMarksDeploymentsScaledToZero(t *testing.T) {
	zero := int32(0)
	one := int32(1)
	client := &Client{client: fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "disabled", Namespace: "example"},
			Spec: appsv1.DeploymentSpec{Replicas: &zero, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "application", Image: "registry.example.com/team/missing:1"}},
			}}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "active", Namespace: "example"},
			Spec: appsv1.DeploymentSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "application", Image: "registry.example.com/team/active:1"}},
			}}},
		},
	).AppsV1()}

	images, err := client.ListDeploymentImages(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].AppName != "active" || images[0].ScaledToZero || images[1].AppName != "disabled" || !images[1].ScaledToZero {
		t.Fatalf("images = %#v", images)
	}
}
