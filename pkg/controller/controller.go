package controller

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/water-hole/ansible-operator/pkg/events"
	"github.com/water-hole/ansible-operator/pkg/runner"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crthandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Options - options for your controller
type Options struct {
	EventHandlers []events.EventHandler
	LoggingLevel  events.LogLevel
	Runner        runner.Runner
	Namespace     string
	GVK           schema.GroupVersionKind
	//StopChannel is need to deal with the bug:
	// https://github.com/kubernetes-sigs/controller-runtime/issues/103
	StopChannel <-chan struct{}
}

// Add - Creates a new ansible operator controller and adds it to the manager
func Add(mgr manager.Manager, options Options) {
	logrus.Infof("Watching %s/%v, %s, %s", options.GVK.Group, options.GVK.Version, options.GVK.Kind, options.Namespace)
	if options.EventHandlers == nil {
		options.EventHandlers = []events.EventHandler{}
	}
	eventHandlers := append(options.EventHandlers, events.NewLoggingEventHandler(options.LoggingLevel))

	h := &AnsibleOperatorReconciler{
		Client:        mgr.GetClient(),
		GVK:           options.GVK,
		Runner:        options.Runner,
		EventHandlers: eventHandlers,
	}

	// Register the GVK with the schema
	mgr.GetScheme().AddKnownTypeWithName(options.GVK, &unstructured.Unstructured{})
	metav1.AddToGroupVersion(mgr.GetScheme(), schema.GroupVersion{
		Group:   options.GVK.Group,
		Version: options.GVK.Version,
	})

	//Create new controller runtime controller and set the controller to watch GVK.
	c, err := controller.New(fmt.Sprintf("%v-controller", strings.ToLower(options.GVK.Kind)), mgr, controller.Options{
		Reconciler: h,
	})
	if err != nil {
		log.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(options.GVK)
	if err := c.Watch(&source.Kind{Type: u}, &crthandler.EnqueueRequestForObject{}); err != nil {
		log.Fatal(err)
	}
	r := NewReconcileLoop(time.Duration(time.Minute)*1, options.GVK, mgr.GetClient())
	r.Stop = options.StopChannel
	cs := &source.Channel{Source: r.Source}
	cs.InjectStopChannel(options.StopChannel)
	if err := c.Watch(cs, &crthandler.EnqueueRequestForObject{}); err != nil {
		log.Fatal(err)
	}
	r.Start()
}
