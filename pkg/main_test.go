package utils

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	v1 "my.domain/guestbook/api/v1"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestMain(m *testing.M) {
	var err error
	ctx, cancel, cfg, k8sClient, testEnv, err = InitK8s()
	if err != nil {
		ctrl.Log.Error(err, "init k8s failed")
		os.Exit(1)
	}
	ctrl.Log.Info("Successfully initialized k8s config")

	os.Exit(m.Run())
}

func InitK8s() (context.Context, context.CancelFunc, *rest.Config, client.Client, *envtest.Environment, error) {
	var (
		cfg       *rest.Config
		k8sClient client.Client
		testEnv   *envtest.Environment
		ctx       context.Context
		cancel    context.CancelFunc
		err       error
	)

	flag.Parse()

	ctx, cancel = context.WithCancel(context.TODO())
	ctrl.Log.Info("bootstrapping test environment")
	testEnv = &envtest.Environment{}

	cfg, err = testEnv.Start()
	if err != nil {
		return ctx, cancel, cfg, k8sClient, testEnv, err
	}
	if cfg == nil {
		return ctx, cancel, cfg, k8sClient, testEnv, fmt.Errorf("cfg cannot be nil")
	}

	err = v1.AddToScheme(scheme.Scheme)
	if err != nil {
		return ctx, cancel, cfg, k8sClient, testEnv, err
	}

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return ctx, cancel, cfg, k8sClient, testEnv, err
	}
	if k8sClient == nil {
		return ctx, cancel, cfg, k8sClient, testEnv, fmt.Errorf("k8sClient cannot be nil")
	}

	return ctx, cancel, cfg, k8sClient, testEnv, err
}
