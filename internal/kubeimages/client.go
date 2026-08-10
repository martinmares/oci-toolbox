package kubeimages

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Options struct {
	Kubeconfig string
	Context    string
	Insecure   bool
}

type Client struct {
	client appsv1.AppsV1Interface
}

type DeploymentImage struct {
	AppName       string
	ContainerName string
	Image         string
	ScaledToZero  bool
}

func New(opts Options) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.Kubeconfig != "" {
		rules.ExplicitPath = opts.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: opts.Context}
	if opts.Insecure {
		overrides.ClusterInfo = clientcmdapi.Cluster{InsecureSkipTLSVerify: true}
	}
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	restConfig, err := config.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	client, err := appsv1.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return &Client{client: client}, nil
}

func (c *Client) ListImages(ctx context.Context, namespace string) ([]string, error) {
	deployments, err := c.client.Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	images := make([]string, 0)
	for _, deployment := range deployments.Items {
		podSpec := deployment.Spec.Template.Spec
		for _, container := range podSpec.InitContainers {
			if container.Image != "" {
				images = append(images, container.Image)
			}
		}
		for _, container := range podSpec.Containers {
			if container.Image != "" {
				images = append(images, container.Image)
			}
		}
	}
	return images, nil
}

func (c *Client) ListDeploymentImages(ctx context.Context, namespace string) ([]DeploymentImage, error) {
	deployments, err := c.client.Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	images := make([]DeploymentImage, 0)
	for _, deployment := range deployments.Items {
		scaledToZero := deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Image == "" {
				continue
			}
			images = append(images, DeploymentImage{
				AppName:       deployment.Name,
				ContainerName: container.Name,
				Image:         container.Image,
				ScaledToZero:  scaledToZero,
			})
		}
	}
	sort.Slice(images, func(i, j int) bool {
		if images[i].AppName != images[j].AppName {
			return images[i].AppName < images[j].AppName
		}
		return images[i].ContainerName < images[j].ContainerName
	})
	return images, nil
}
