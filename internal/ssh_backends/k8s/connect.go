package k8s

import (
	"context"
	"fmt"
	"net/http"

	"github.com/borderzero/border0-cli/internal/ssh_backends/common"
	"github.com/gliderlabs/ssh"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// connect connects returns a net.Conn which under
// the hood is a TTY in a given docker container,
// leveraging Docker's "exec" command.
func connect(
	ctx context.Context,
	config *rest.Config,
	namespace string,
	pod string,
	user string,
	cmd []string,
) (remotecommand.Executor, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	req := clientset.
		CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec")

	option := &v1.PodExecOptions{
		Command: cmd,
		Stdin:   true,
		Stdout:  true,
		Stderr:  true,
		TTY:     true,
	}
	req = req.VersionedParams(option, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, req.URL())
	if err != nil {
		return nil, err
	}

	return exec, nil
}

// GetSshServer returns an ssh server for which the handler
// drops the clients into a shell in a docker container.
func GetSshServer(
	ctx context.Context,
	logger *zap.Logger,
	signerPrincipal string,
	signerPub []byte,
	namespace string,
	pod string,
	user string,
	shell string,
) (*ssh.Server, error) {
	signers, err := common.GetSigners()
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate signers: %v", err)
	}
	publicKeyHandler, err := common.GetPublicKeyHandler(ctx, logger, signerPrincipal, signerPub)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key handler: %v", err)
	}
	return &ssh.Server{
		Version:           "kubectl-exec-proxy-ssh-server",
		HostSigners:       signers,
		Handler:           getK8sExecSshHandler(ctx, logger, namespace, pod, user, shell),
		PublicKeyHandler:  publicKeyHandler,
		RequestHandlers:   ssh.DefaultRequestHandlers,
		ChannelHandlers:   ssh.DefaultChannelHandlers,
		SubsystemHandlers: ssh.DefaultSubsystemHandlers,
	}, nil
}

func getK8sExecSshHandler(
	ctx context.Context,
	logger *zap.Logger,
	namespace string,
	pod string,
	user string,
	shell string,
) ssh.Handler {
	return ssh.Handler(func(s ssh.Session) {
		defer s.Close()

		config, err := clientcmd.BuildConfigFromFlags("", "/Users/adriano/.kube/config")
		if err != nil {
			logger.Error(
				"failed to get k8s config",
				zap.String("namespace", namespace),
				zap.String("pod", pod),
				zap.String("user", user),
				zap.String("shell", shell),
				zap.Error(err),
			)
			return
		}

		exec, err := connect(ctx, config, namespace, pod, user, []string{shell})
		if err != nil {
			logger.Error(
				"failed to get remote command executor for k8s pod",
				zap.String("namespace", namespace),
				zap.String("pod", pod),
				zap.String("user", user),
				zap.String("shell", shell),
				zap.Error(err),
			)
			return
		}

		err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:  s,
			Stdout: s,
			Stderr: s.Stderr(),
		})
		if err != nil {
			logger.Error(
				"failed to stream remote command executor",
				zap.String("namespace", namespace),
				zap.String("pod", pod),
				zap.String("user", user),
				zap.String("shell", shell),
				zap.Error(err),
			)
			return
		}

		<-ctx.Done()
	})
}
