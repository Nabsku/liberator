# PVC Finalizer Controller

This project is a Kubernetes controller built using the [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) library. It manages the cleanup of `PersistentVolumeClaim` (PVC) resources by removing their references from associated `PersistentVolume` (PV) resources when the PVC is deleted.

## Features

- Automatically clears the `claimRef` field in a `PersistentVolume` when its associated `PersistentVolumeClaim` is deleted.
- Ensures proper cleanup by using a custom finalizer (`liberator.io/pv-claim-ref-cleanup`) on PVCs.
- Provides health and readiness probes for monitoring the controller's status.

## Prerequisites

- Kubernetes cluster (v1.20+ recommended)
- Go (v1.19+)
- Controller-runtime library

## Installation

1. Clone the repository:

   ```bash
   git clone <repository-url>
   cd <repository-directory>
   ```

2. Build the controller:

   ```bash
   go build -o pvc-controller main.go
   ```

3. Deploy the controller to your Kubernetes cluster. Ensure the necessary RBAC permissions are configured.

## Usage

### Flags

- `--health-probe-bind-address`: Address for health and readiness probes (default: `:8081`).
- `--leader-elect`: Enable leader election for high availability (default: `false`).

### Adding the Finalizer

The controller does not automatically add the finalizer to PVCs. Users must manually add the finalizer `liberator.io/pv-claim-ref-cleanup` to PVCs that require cleanup.

Example:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: example-pvc
  namespace: default
  finalizers:
    - liberator.io/pv-claim-ref-cleanup
spec: ...
```

## Code Overview

### Main Components

1. **`main.go`**:

   - Initializes the controller manager.
   - Configures health and readiness probes.
   - Sets up the `PVCReconciler`.

2. **`PVCReconciler`**:

   - Watches for PVC events.
   - Handles PVC deletion by:
     - Clearing the `claimRef` field in the associated PV.
     - Removing the custom finalizer from the PVC.

3. **Helper Functions**:
   - `hasFinalizer`: Checks if a PVC has the custom finalizer.
   - `removeFinalizer`: Removes the custom finalizer from a PVC.

### Reconciliation Logic

- When a PVC with the custom finalizer is deleted:
  1. The controller fetches the associated PV.
  2. If the PV's `claimRef` matches the PVC, it clears the `claimRef`.
  3. The finalizer is removed from the PVC, allowing it to be deleted.

## Health and Readiness Probes

- **Health Probe**: `/healthz`
- **Readiness Probe**: `/readyz`

These endpoints can be used to monitor the controller's status.

## Development

### Running Locally

1. Set up a Kubernetes cluster (e.g., using [kind](https://kind.sigs.k8s.io/)).
2. Run the controller locally:
   ```bash
   go run main.go
   ```

### Testing

- Unit tests can be added for the reconciliation logic and helper functions.
- Use tools like [envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest) for integration testing.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## License

This project is licensed under the MIT License.
