package main

import (
	"context"
	"flag"
	"os"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// Constants
const (
	PVClaimRefCleanupFinalizer = "liberator.io/pv-claim-ref-cleanup"
)

var (
	scheme                  = runtime.NewScheme()
	setupLog                = ctrl.Log.WithName("setup")
	enableLeaderElection    bool
	probeAddr               string
	maxConcurrentReconciles int
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

func main() {
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 3, "The maximum number of concurrent reconciles for the controller.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "liberator-leader-election",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&PVCReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("PVC"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PVC")
		os.Exit(1)
	}

	// Add health check endpoints
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// PVCReconciler reconciles a PVC object
type PVCReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// Reconcile processes PVC events
func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("pvc", req.NamespacedName)
	logger.Info("Received Event for PVC")

	// Get the PVC
	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		// The PVC no longer exists, which means it's been deleted
		// No need to requeue
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If PVC is being deleted and has our finalizer, handle the cleanup
	if !pvc.DeletionTimestamp.IsZero() && hasFinalizer(pvc.Finalizers) {
		return r.handlePVCDeletion(ctx, &pvc, logger)
	}

	// If it's not being deleted or doesn't have our finalizer, nothing to do
	// We don't add finalizers as per requirements - users will add them
	return ctrl.Result{}, nil
}

// handlePVCDeletion handles the PVC deletion reconciliation
func (r *PVCReconciler) handlePVCDeletion(ctx context.Context, pvc *corev1.PersistentVolumeClaim, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing PVC deletion", "volumeName", pvc.Spec.VolumeName)

	// If this PVC has a PV bound to it, clean the claimRef
	if pvc.Spec.VolumeName != "" {
		// Get the PV
		var pv corev1.PersistentVolume
		if err := r.Get(ctx, client.ObjectKey{Name: pvc.Spec.VolumeName}, &pv); err != nil {
			logger.Error(err, "Failed to get PV", "pvName", pvc.Spec.VolumeName)
			// If the PV doesn't exist or there was another error getting it,
			// we can still proceed with finalizer removal
			if client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		} else {
			// If PV exists and has a claimRef to our PVC
			if pv.Spec.ClaimRef != nil &&
				pv.Spec.ClaimRef.Name == pvc.Name &&
				pv.Spec.ClaimRef.Namespace == pvc.Namespace {

				logger.Info("Clearing claimRef from PV", "pvName", pv.Name)

				// Create a copy of the PV and clear the claimRef
				pvCopy := pv.DeepCopy()
				pvCopy.Spec.ClaimRef = nil

				// Update the PV
				if err := r.Update(ctx, pvCopy); err != nil {
					logger.Error(err, "Failed to update PV", "pvName", pv.Name)
					return ctrl.Result{}, err
				}

				logger.Info("Successfully cleared claimRef from PV", "pvName", pv.Name)
			}
		}
	}

	// Remove our finalizer to allow the PVC to be deleted
	return r.removeFinalizer(ctx, pvc, logger)
}

// removeFinalizer removes our finalizer from the PVC
func (r *PVCReconciler) removeFinalizer(ctx context.Context, pvc *corev1.PersistentVolumeClaim, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Removing finalizer from PVC")

	// Create a copy and remove the finalizer
	pvcCopy := pvc.DeepCopy()
	pvcCopy.Finalizers = removeFinalizer(pvcCopy.Finalizers)

	// Update the PVC
	if err := r.Update(ctx, pvcCopy); err != nil {
		logger.Error(err, "Failed to remove finalizer from PVC")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully removed finalizer from PVC")
	return ctrl.Result{}, nil
}

// hasFinalizer checks if the PVC has our finalizer
func hasFinalizer(finalizers []string) bool {
	return slices.Contains(finalizers, PVClaimRefCleanupFinalizer)
}

// removeFinalizer returns a new slice of finalizers without our finalizer
func removeFinalizer(finalizers []string) []string {
	result := []string{}
	for _, finalizer := range finalizers {
		if finalizer != PVClaimRefCleanupFinalizer {
			result = append(result, finalizer)
		}
	}
	return result
}

func (r *PVCReconciler) SetupWithManager(mgr manager.Manager) error {
	hasBoundPVPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		// Skip PVCs without a bound PV
		if pvc, ok := obj.(*corev1.PersistentVolumeClaim); ok {
			return pvc.Spec.VolumeName != ""
		}
		return false
	})

	predicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return hasFinalizer(e.Object.GetFinalizers())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObject := hasFinalizer(e.ObjectOld.GetFinalizers())
			newObject := hasFinalizer(e.ObjectNew.GetFinalizers())

			deletionStarted := !e.ObjectNew.GetDeletionTimestamp().IsZero()

			return oldObject != newObject || deletionStarted
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return hasFinalizer(e.Object.GetFinalizers())
		},
	}

	combined := predicate.And(
		hasBoundPVPredicate,
		predicates,
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		WithEventFilter(combined).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(r)
}
