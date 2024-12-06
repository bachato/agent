package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/portainer/portainer/pkg/libstack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// KubeClient can be used to query the Kubernetes API
type KubeClient struct {
	cli *kubernetes.Clientset
}

// NewKubeClient returns a pointer to a new KubeClient instance
func NewKubeClient() (*KubeClient, error) {
	kubeCli := &KubeClient{}

	cli, err := BuildLocalClient()
	if err != nil {
		return nil, err
	}

	kubeCli.cli = cli
	return kubeCli, nil
}

func BuildLocalClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

// StartExecProcess will start an exec process inside a container located inside a pod inside a specific namespace
// using the specified command. The stdin parameter will be bound to the stdin process and the stdout process will write
// to the stdout parameter.
// This function only works against a local endpoint using an in-cluster config.
func (kcl *KubeClient) StartExecProcess(token, namespace, podName, containerName string, command []string, stdin io.Reader, stdout io.Writer) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	if token != "" {
		config.BearerToken = token
		config.BearerTokenFile = ""
	}

	req := kcl.cli.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return err
	}

	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Tty:    true,
	})
	if err != nil {
		var exitError utilexec.ExitError
		if !errors.As(err, &exitError) {
			return errors.New("unable to start exec process")
		}
	}

	return nil
}

type ResourceStatus struct {
	Status  libstack.Status
	Message string
	Err     error
}

func (kcl *KubeClient) GetDeploymentStatus(namespace string, name string) ResourceStatus {
	item, err := kcl.cli.AppsV1().Deployments(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return ResourceStatus{
			Status:  libstack.StatusRemoved,
			Message: fmt.Sprintf("unable to retrieve deployment name=%s namespace=%s", name, namespace),
			Err:     err,
		}
	}

	if item.ObjectMeta.DeletionTimestamp != nil && item.Status.Replicas != 0 {
		return ResourceStatus{
			Status: libstack.StatusRemoving,
		}
	}

	if item.Spec.Replicas != nil && *item.Spec.Replicas == 0 && item.Status.Replicas == 0 {
		return ResourceStatus{
			Status: libstack.StatusStopped,
		}
	}

	// starting | running | completed | error
	return kcl.inferWorkloadStatusByPods(namespace, name, item.Spec.Selector)
}

func (kcl *KubeClient) GetDaemonSetStatus(namespace string, name string) ResourceStatus {
	item, err := kcl.cli.AppsV1().DaemonSets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return ResourceStatus{
			Status:  libstack.StatusRemoved,
			Message: fmt.Sprintf("unable to retrieve daemonset name=%s namespace=%s", name, namespace),
			Err:     err,
		}
	}

	if item.ObjectMeta.DeletionTimestamp != nil && item.Status.CurrentNumberScheduled != 0 {
		return ResourceStatus{
			Status: libstack.StatusRemoving,
		}
	}

	// daemonset cannot be stopped

	// starting | running | completed | error
	return kcl.inferWorkloadStatusByPods(namespace, name, item.Spec.Selector)
}

func (kcl *KubeClient) GetStatefulSetStatus(namespace string, name string) ResourceStatus {
	item, err := kcl.cli.AppsV1().StatefulSets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return ResourceStatus{
			Status:  libstack.StatusRemoved,
			Message: fmt.Sprintf("unable to retrieve statefulset name=%s namespace=%s", name, namespace),
			Err:     err,
		}
	}

	if item.ObjectMeta.DeletionTimestamp != nil && item.Status.Replicas != 0 {
		return ResourceStatus{
			Status: libstack.StatusRemoving,
		}
	}

	if item.Spec.Replicas != nil && *item.Spec.Replicas == 0 && item.Status.Replicas == 0 {
		return ResourceStatus{
			Status: libstack.StatusStopped,
		}
	}

	// starting | running | completed | error
	return kcl.inferWorkloadStatusByPods(namespace, name, item.Spec.Selector)
}

func (kcl *KubeClient) GetPodStatus(namespace string, name string) ResourceStatus {
	item, err := kcl.cli.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return ResourceStatus{
			Status:  libstack.StatusRemoved,
			Message: fmt.Sprintf("unable to retrieve pod name=%s namespace=%s", name, namespace),
			Err:     err,
		}
	}

	if item.ObjectMeta.DeletionTimestamp != nil {
		return ResourceStatus{
			Status: libstack.StatusRemoving,
		}
	}
	// pod cannot be stopped

	// starting | running | completed | error
	return kcl.inferWorkloadStatusByPods(namespace, name, metav1.SetAsLabelSelector(item.Labels))
}

// retrieve the starting | running | completed | error status of the workload based on pods states
func (kcl *KubeClient) inferWorkloadStatusByPods(namespace string, name string, selector *metav1.LabelSelector) ResourceStatus {
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return ResourceStatus{
			Status:  libstack.StatusError,
			Message: fmt.Sprintf("unable to convert workload labelselector of name=%s namespace=%s to pods selector", name, namespace),
			Err:     err,
		}
	}

	pods, err := kcl.cli.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: labelSelector.String()})
	if err != nil {
		return ResourceStatus{
			Status:  libstack.StatusRemoved,
			Message: fmt.Sprintf("unable to retrieve the pods list for name=%s namespace=%s", name, namespace),
			Err:     err,
		}
	}

	containerStatuses := make([]ResourceStatus, 0)

	for _, pod := range pods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.Ready || status.State.Running != nil {
				containerStatuses = append(containerStatuses, ResourceStatus{
					Status: libstack.StatusRunning,
				})
				continue
			}

			if status.RestartCount == 0 && status.State.Waiting != nil {
				containerStatuses = append(containerStatuses, ResourceStatus{
					Status:  libstack.StatusStarting,
					Message: status.State.Waiting.Message,
				})
				continue
			}

			state := status.LastTerminationState.Terminated
			if status.State.Terminated != nil {
				state = status.State.Terminated
			}

			if state != nil {
				reason := podLastTerminationReasonToLibstackStatus(state.Reason)
				message := podMessageFromContainerState(reason, status.State)
				containerStatuses = append(containerStatuses, ResourceStatus{
					Status:  reason,
					Message: message,
				})
				continue
			}

			containerStatuses = append(containerStatuses, ResourceStatus{
				Status: libstack.StatusUnknown,
			})
		}
	}

	return AggregateStatuses(containerStatuses)
}

func AggregateStatuses(statuses []ResourceStatus) ResourceStatus {
	statusCounts := make(map[libstack.Status]int)
	totalCount := len(statuses)
	errorMessage := ""

	for _, status := range statuses {
		statusCounts[status.Status] += 1
		if status.Status == libstack.StatusError {
			errorMessage += status.Message + "\n"
		}
	}

	switch {
	case statusCounts[libstack.StatusError] > 0:
		return ResourceStatus{Status: libstack.StatusError, Message: errorMessage}
	case statusCounts[libstack.StatusStarting] > 0:
		return ResourceStatus{Status: libstack.StatusStarting}
	case statusCounts[libstack.StatusRemoving] > 0:
		return ResourceStatus{Status: libstack.StatusRemoving}
	case statusCounts[libstack.StatusCompleted] == totalCount:
		return ResourceStatus{Status: libstack.StatusCompleted}
	case statusCounts[libstack.StatusRunning]+statusCounts[libstack.StatusCompleted] == totalCount:
		return ResourceStatus{Status: libstack.StatusRunning}
	case statusCounts[libstack.StatusStopped]+statusCounts[libstack.StatusCompleted] == totalCount:
		return ResourceStatus{Status: libstack.StatusStopped}
	case statusCounts[libstack.StatusRemoved]+statusCounts[libstack.StatusCompleted] == totalCount:
		return ResourceStatus{Status: libstack.StatusRemoved}
	default:
		return ResourceStatus{Status: libstack.StatusUnknown}
	}
}

func podLastTerminationReasonToLibstackStatus(reason string) libstack.Status {
	// we may need to detect other reasons in specific scenarios and match them to libstack Statuses
	switch reason {
	case "Completed":
		return libstack.StatusCompleted
	case "Error":
		return libstack.StatusError
	default:
		return libstack.StatusUnknown
	}
}

func podMessageFromContainerState(status libstack.Status, state corev1.ContainerState) string {
	if status == libstack.StatusCompleted {
		return ""
	}

	if state.Terminated != nil {
		return state.Terminated.Reason + " " + state.Terminated.Message
	}

	if state.Waiting != nil {
		return state.Waiting.Reason + " " + state.Waiting.Message
	}

	return ""
}
