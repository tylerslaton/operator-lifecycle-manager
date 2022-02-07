package main

import (
	"context"
	"fmt"
	"log"

	kuberpakv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/api/v1alpha1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	lister "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-registry/pkg/client"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type RegistryClientProvider struct {
	Refs map[registry.CatalogKey]client.Interface
}

func (rcp RegistryClientProvider) ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface {
	return rcp.Refs
}

const addr string = "localhost:50051"

func main() {
	ctx := context.Background()

	rcp := RegistryClientProvider{
		Refs: make(map[registry.CatalogKey]client.Interface),
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		log.Fatalf("failed add to scheme with err: %s", err)
	}
	if err := kuberpakv1alpha1.AddToScheme(scheme); err != nil {
		log.Fatalf("failed add to scheme with err: %s", err)
	}
	cfg := ctrl.GetConfigOrDie()
	kcli, err := k8scontrollerclient.New(cfg, k8scontrollerclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Fatalf("failed add to scheme with err: %s", err)
	}

	cs := &v1alpha1.CatalogSourceList{}

	if err := kcli.List(ctx, cs); err != nil {
		log.Fatalf("failed to list with err: %s", err)
	}

	for _, item := range cs.Items {
		catkey := registry.CatalogKey{
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
		}

		if k, ok := rcp.Refs[catkey]; ok {
			log.Printf("found duplicate key: %v", k)
			continue
		}

		cli, err := client.NewClient(addr)
		if err != nil {
			log.Fatalf("failed create new client with err: %s", err)
		}

		rcp.Refs[catkey] = cli
	}

	provider := resolver.SourceProviderFromRegistryClientProvider(
		rcp,
		logrus.New(),
	)

	solver := resolver.NewDefaultSatResolver(
		provider,
		lister.NewLister().OperatorsV1alpha1().CatalogSourceLister(),
		logrus.New(),
	)

	csvList := &v1alpha1.ClusterServiceVersionList{}
	if err := kcli.List(ctx, csvList); err != nil {
		log.Fatalf("failed list csvs with err: %s", err)
	}
	csvs := []*v1alpha1.ClusterServiceVersion{}
	for _, csv := range csvList.Items {
		csvs = append(csvs, &csv)
	}

	subList := &v1alpha1.SubscriptionList{}
	if err := kcli.List(ctx, subList); err != nil {
		log.Fatalf("failed list subs with err: %s", err)
	}
	subs := []*v1alpha1.Subscription{}
	for _, sub := range subList.Items {
		subs = append(subs, &sub)
	}

	// Get currently installed operators in the operators namespace "operators"
	ops, err := solver.SolveOperators([]string{"operators"}, csvs, subs)
	if err != nil {
		log.Fatalf("failed get ops with err: %s", err)
	}

	for _, entry := range ops {
		bundle := &kuberpakv1alpha1.Bundle{}
		err := kcli.Get(ctx, types.NamespacedName{Name: entry.Name}, bundle)
		if err != nil {
			if !errors.IsNotFound(err) {
				log.Fatalf("failed to get bundle with err %v", err)
			}
			bundle = &kuberpakv1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name: entry.Name,
					Annotations: map[string]string{
						"subscription": fmt.Sprintf("%s-%s", entry.Package(), entry.Channel()),
					},
				},
				Spec: kuberpakv1alpha1.BundleSpec{
					ProvisionerClassName: "testprov",
					Image:                entry.BundlePath,
				},
			}
			if err := kcli.Create(ctx, bundle); err != nil {
				log.Fatalf("failed to create bundle :( : %v", err)
			}
		}
	}
}
