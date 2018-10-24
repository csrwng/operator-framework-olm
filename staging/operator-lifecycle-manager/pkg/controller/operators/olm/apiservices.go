package olm

import (
	"fmt"
	"strings"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

const (
	// DefaultCertMinFresh is the default min-fresh value - 1 day
	DefaultCertMinFresh = time.Hour * 24
	// DefaultCertValidFor is the default duration a cert can be valid for - 2 years
	DefaultCertValidFor = time.Hour * 24 * 730
	// OLMCAHashAnnotationKey is the label key used to store the hash of the CA cert
	OLMCAHashAnnotationKey = "olmcahash"
)

func (a *Operator) syncAPIServices(obj interface{}) (syncError error) {
	apiService, ok := obj.(*apiregistrationv1.APIService)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting APIService failed")
	}
	if ownerutil.IsOwnedByKind(apiService, v1alpha1.ClusterServiceVersionKind) {
		oref := ownerutil.GetOwnerByKind(apiService, v1alpha1.ClusterServiceVersionKind)
		log.Infof("APIService %s change requeuing CSV %s", apiService.GetName(), oref.Name)
		a.requeueCSV(oref.Name, apiService.Spec.Service.Namespace)
	}

	return nil
}

func (a *Operator) shouldRotateCerts(csv *v1alpha1.ClusterServiceVersion) bool {
	now := metav1.Now()
	if !csv.Status.CertsRotateAt.IsZero() && csv.Status.CertsRotateAt.Before(&now) {
		return true
	}

	return false
}

// checkAPIServiceResources checks if all expected generated resources for the given APIService exist
func (a *Operator) checkAPIServiceResources(csv *v1alpha1.ClusterServiceVersion, hashFunc certs.PEMHash) error {
	for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
		apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
		logger := log.WithFields(log.Fields{
			"csv":        csv.GetName(),
			"namespace":  csv.GetNamespace(),
			"apiservice": apiServiceName,
		})

		serviceName := APIServiceNameToServiceName(apiServiceName)
		service, err := a.lister.CoreV1().ServiceLister().Services(csv.GetNamespace()).Get(serviceName)
		if k8serrors.IsNotFound(err) || err != nil {
			logger.Warnf("generated Service not found")
			return err
		}

		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
		if k8serrors.IsNotFound(err) || err != nil {
			logger.Warnf("generated APIService not found")
			return err
		}

		// Check if the APIService points to the correct service
		if apiService.Spec.Service.Name != serviceName || apiService.Spec.Service.Namespace != csv.GetNamespace() {
			logger.Warnf("APIService service reference mismatch")
			return fmt.Errorf("APIService service reference mismatch")
		}

		// Check if CA is Active
		caBundle := apiService.Spec.CABundle
		ca, err := certs.PEMToCert(caBundle)
		if err != nil {
			logger.Warnf("could not convert APIService CA bundle to x509 cert")
			return err
		}
		if !certs.Active(ca) {
			logger.Warnf("CA cert not active")
			return fmt.Errorf("CA cert not active")
		}

		// Check if serving cert is active
		secretName := apiServiceName + "-cert"
		secret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(secretName)
		if k8serrors.IsNotFound(err) || err != nil {
			logger.Warnf("generated Secret %s could not be retrieved", secretName)
			return err
		}
		cert, err := certs.PEMToCert(secret.Data["tls.crt"])
		if err != nil {
			logger.Warnf("could not convert serving cert to x509 cert")
			return err
		}
		if !certs.Active(cert) {
			logger.Warnf("serving cert not active")
			return fmt.Errorf("serving cert not active")
		}

		// Check if CA hash matches expected
		caHash := hashFunc(caBundle)
		if hash, ok := secret.GetAnnotations()[OLMCAHashAnnotationKey]; !ok || hash != caHash {
			logger.Warnf("secret %s CA cert hash does not match expected", secretName)
			return fmt.Errorf("secret %s CA cert hash does not match expected", secretName)
		}

		// Check if serving cert is trusted by the CA
		hosts := []string{
			fmt.Sprintf("%s.%s", service.GetName(), csv.GetNamespace()),
			fmt.Sprintf("%s.%s.svc", service.GetName(), csv.GetNamespace()),
		}
		for _, host := range hosts {
			if err := certs.VerifyCert(ca, cert, host); err != nil {
				return fmt.Errorf("could not verify cert: %s", err.Error())
			}
		}

		// Ensure the existing deployment has a matching CA hash annotation
		deployment, err := a.lister.AppsV1().DeploymentLister().Deployments(csv.GetNamespace()).Get(desc.DeploymentName)
		if k8serrors.IsNotFound(err) || err != nil {
			logger.Warnf("expected APIService Deployment %s could not be retrieved", desc.DeploymentName)
			return err
		}
		if hash, ok := deployment.GetAnnotations()[OLMCAHashAnnotationKey]; !ok || hash != caHash {
			logger.Warnf("Deployment %s CA cert hash does not match expected", desc.DeploymentName)
			return fmt.Errorf("Deployment %s CA cert hash does not match expected", desc.DeploymentName)
		}
	}

	return nil
}

func (a *Operator) isAPIServiceAvailable(apiService *apiregistrationv1.APIService) bool {
	for _, c := range apiService.Status.Conditions {
		if c.Type == apiregistrationv1.Available && c.Status == apiregistrationv1.ConditionTrue {
			return true
		}
	}
	return false
}

func (a *Operator) areAPIServicesAvailable(descs []v1alpha1.APIServiceDescription) (bool, error) {
	for _, desc := range descs {
		apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
		if err != nil {
			return false, err
		}

		if !a.isAPIServiceAvailable(apiService) {
			return false, fmt.Errorf("APIService %s not available", apiService.GetName())
		}

		if err := a.isGVKRegistered(desc.Group, desc.Version, desc.Kind); err != nil {
			return false, fmt.Errorf("group: %s, version: %s, kind: %s not registered", desc.Group, desc.Version, desc.Kind)
		}
	}

	return true, nil
}

func (a *Operator) installOwnedAPIServiceRequirements(csv *v1alpha1.ClusterServiceVersion, strategy install.Strategy) (install.Strategy, error) {
	logger := log.WithFields(log.Fields{
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
	})

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	// Return early if there are no owned APIServices
	if len(csv.Spec.APIServiceDefinitions.Owned) == 0 {
		return strategyDetailsDeployment, nil
	}

	// Create the CA
	expiration := time.Now().Add(DefaultCertValidFor)
	ca, err := certs.GenerateCA(expiration)
	if err != nil {
		logger.Debug("failed to generate CA")
		return nil, err
	}
	rotateAt := expiration.Add(-1 * DefaultCertMinFresh)

	depSpecs := make(map[string]appsv1.DeploymentSpec)
	for _, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		depSpecs[sddSpec.Name] = sddSpec.Spec
	}

	// Create all resources required, and update the matching DeploymentSpec's Volume and VolumeMounts
	apiDescs := csv.GetOwnedAPIServiceDescriptions()
	for _, desc := range apiDescs {
		depSpec, ok := depSpecs[desc.DeploymentName]
		if !ok {
			return nil, fmt.Errorf("StrategyDetailsDeployment missing deployment %s for owned APIService %s", desc.DeploymentName, fmt.Sprintf("%s.%s", desc.Version, desc.Group))
		}

		newDepSpec, err := a.installAPIServiceRequirements(desc, ca, rotateAt, depSpec, csv)
		if err != nil {
			return nil, err
		}
		depSpecs[desc.DeploymentName] = *newDepSpec
	}

	// Replace all matching DeploymentSpecs in the strategy
	for i, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		if depSpec, ok := depSpecs[sddSpec.Name]; ok {
			strategyDetailsDeployment.DeploymentSpecs[i].Spec = depSpec
		}
	}

	// Set csv cert status
	csv.Status.CertsLastUpdated = metav1.Now()
	csv.Status.CertsRotateAt = metav1.NewTime(rotateAt)

	return strategyDetailsDeployment, nil
}

func (a *Operator) installAPIServiceRequirements(desc v1alpha1.APIServiceDescription, ca *certs.KeyPair, rotateAt time.Time, depSpec appsv1.DeploymentSpec, csv *v1alpha1.ClusterServiceVersion) (*appsv1.DeploymentSpec, error) {
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
	logger := log.WithFields(log.Fields{
		"csv":        csv.GetName(),
		"namespace":  csv.GetNamespace(),
		"apiservice": apiServiceName,
	})

	// create a service for the deployment
	containerPort := 443
	if desc.ContainerPort > 0 {
		containerPort = int(desc.ContainerPort)
	}
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       int32(443),
					TargetPort: intstr.FromInt(containerPort),
				},
			},
			Selector: depSpec.Selector.MatchLabels,
		},
	}
	service.SetName(APIServiceNameToServiceName(apiServiceName))
	service.SetNamespace(csv.GetNamespace())
	ownerutil.AddNonBlockingOwner(service, csv)

	existingService, err := a.lister.CoreV1().ServiceLister().Services(csv.GetNamespace()).Get(service.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if !ownerutil.Adoptable(csv, existingService.GetOwnerReferences()) {
			return nil, fmt.Errorf("service %s not safe to replace: extraneous ownerreferences found", service.GetName())
		}

		// Append previous ownerrefs to the new Service
		service.SetOwnerReferences(append(service.GetOwnerReferences(), existingService.GetOwnerReferences()...))

		// Attempt a replace
		deleteErr := a.OpClient.DeleteService(service.GetNamespace(), service.GetName(), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(deleteErr) {
			return nil, fmt.Errorf("could not delete existing service %s", service.GetName())
		}
	}

	// Attempt to create the Service
	_, err = a.OpClient.CreateService(service)
	if err != nil {
		logger.Warnf("could not create service %s", service.GetName())
		return nil, fmt.Errorf("could not create service %s: %s", service.GetName(), err.Error())
	}

	// Create signed serving cert
	hosts := []string{
		fmt.Sprintf("%s.%s", service.GetName(), csv.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", service.GetName(), csv.GetNamespace()),
	}
	servingPair, err := certs.CreateSignedServingPair(rotateAt, ca, hosts)
	if err != nil {
		logger.Warnf("could not generate signed certs for hosts %v", hosts)
		return nil, err
	}

	// Create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert serving certificate and private key to PEM format for APIService %s", apiServiceName)
		return nil, err
	}

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": privPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(apiServiceName + "-cert")
	secret.SetNamespace(csv.GetNamespace())

	// Add olmcasha hash as a label to the
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for APIService %s", apiServiceName)
		return nil, err
	}
	caHash := certs.PEMSHA256(caPEM)
	secret.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	existingSecret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(secret.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecret.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secret, csv)
		}

		// Attempt an update
		if _, err := a.OpClient.UpdateSecret(secret); err != nil {
			logger.Warnf("could not update secret %s", secret.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the secret
		ownerutil.AddNonBlockingOwner(secret, csv)
		_, err = a.OpClient.CreateSecret(secret)
		if err != nil {
			log.Warnf("could not create secret %s", secret.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// create Role and RoleBinding to allow the deployment to mount the Secret
	secretRole := &rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secret.GetName()},
			},
		},
	}
	secretRole.SetName(secret.GetName())
	secretRole.SetNamespace(csv.GetNamespace())

	existingSecretRole, err := a.lister.RbacV1().RoleLister().Roles(csv.GetNamespace()).Get(secretRole.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecretRole.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRole, csv)
		}

		// Attempt an update
		if _, err := a.OpClient.UpdateRole(secretRole); err != nil {
			logger.Warnf("could not update secret role %s", secretRole.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRole, csv)
		_, err = a.OpClient.CreateRole(secretRole)
		if err != nil {
			log.Warnf("could not create secret role %s", secretRole.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	secretRoleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     secretRole.GetName(),
		},
	}
	secretRoleBinding.SetName(secret.GetName())
	secretRoleBinding.SetNamespace(csv.GetNamespace())

	existingSecretRoleBinding, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(csv.GetNamespace()).Get(secretRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecretRoleBinding.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
		}

		// Attempt an update
		if _, err := a.OpClient.UpdateRoleBinding(secretRoleBinding); err != nil {
			logger.Warnf("could not update secret rolebinding %s", secretRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
		_, err = a.OpClient.CreateRoleBinding(secretRoleBinding)
		if err != nil {
			log.Warnf("could not create secret rolebinding %s", secretRoleBinding.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// create ClusterRoleBinding to system:auth-delegator Role
	authDelegatorClusterRoleBinding := &rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:auth-delegator",
		},
	}
	authDelegatorClusterRoleBinding.SetName(apiServiceName + "-system:auth-delegator")

	existingAuthDelegatorClusterRoleBinding, err := a.lister.RbacV1().ClusterRoleBindingLister().Get(authDelegatorClusterRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingAuthDelegatorClusterRoleBinding.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(authDelegatorClusterRoleBinding, csv)
		}

		// Attempt an update
		if _, err := a.OpClient.UpdateClusterRoleBinding(authDelegatorClusterRoleBinding); err != nil {
			logger.Warnf("could not update auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(authDelegatorClusterRoleBinding, csv)
		_, err = a.OpClient.CreateClusterRoleBinding(authDelegatorClusterRoleBinding)
		if err != nil {
			log.Warnf("could not create auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// create RoleBinding to extension-apiserver-authentication-reader Role in the kube-system namespace
	authReaderRoleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "extension-apiserver-authentication-reader",
		},
	}
	authReaderRoleBinding.SetName(apiServiceName + "-auth-reader")
	authReaderRoleBinding.SetNamespace("kube-system")

	existingAuthReaderRoleBinding, err := a.lister.RbacV1().RoleBindingLister().RoleBindings("kube-system").Get(authReaderRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingAuthReaderRoleBinding.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(authReaderRoleBinding, csv)
		}

		// Attempt an update
		if _, err := a.OpClient.UpdateRoleBinding(authReaderRoleBinding); err != nil {
			logger.Warnf("could not update auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(authReaderRoleBinding, csv)
		_, err = a.OpClient.CreateRoleBinding(authReaderRoleBinding)
		if err != nil {
			log.Warnf("could not create auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// update deployment with secret volume mount
	volume := corev1.Volume{
		Name: "apiservice-cert",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secret.GetName(),
				Items: []corev1.KeyToPath{
					{
						Key:  "tls.crt",
						Path: "apiserver.crt",
					},
					{
						Key:  "tls.key",
						Path: "apiserver.key",
					},
				},
			},
		},
	}

	replaced := false
	for i, v := range depSpec.Template.Spec.Volumes {
		if v.Name == volume.Name {
			depSpec.Template.Spec.Volumes[i] = volume
			replaced = true
			break
		}
	}
	if !replaced {
		depSpec.Template.Spec.Volumes = append(depSpec.Template.Spec.Volumes, volume)
	}

	mount := corev1.VolumeMount{
		Name:      volume.Name,
		MountPath: "/apiserver.local.config/certificates",
	}
	for i, container := range depSpec.Template.Spec.Containers {
		found := false
		for j, m := range container.VolumeMounts {
			if m.Name == mount.Name {
				found = true
				break
			}

			// replace if mounting to the same location
			if m.MountPath == mount.MountPath {
				container.VolumeMounts[j] = mount
				found = true
				break
			}
		}
		if !found {
			container.VolumeMounts = append(container.VolumeMounts, mount)
		}

		depSpec.Template.Spec.Containers[i] = container
	}

	// setting the olm hash label forces a rollout and ensures that the new secret
	// is used by the apiserver if not hot reloading
	depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	exists := true
	apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, err
		}

		exists = false
		apiService = &apiregistrationv1.APIService{
			Spec: apiregistrationv1.APIServiceSpec{
				Group:                desc.Group,
				Version:              desc.Version,
				GroupPriorityMinimum: int32(2000),
				VersionPriority:      int32(15),
			},
		}
		apiService.SetName(apiServiceName)
	} else {
		// check if the APIService is adoptable
		if !ownerutil.Adoptable(csv, apiService.GetOwnerReferences()) {
			return nil, fmt.Errorf("pre-existing APIService %s is not adoptable", apiServiceName)
		}
	}

	// Add the CSV as an owner
	ownerutil.AddNonBlockingOwner(apiService, csv)

	// update the ServiceReference
	apiService.Spec.Service = &apiregistrationv1.ServiceReference{
		Namespace: service.GetNamespace(),
		Name:      service.GetName(),
	}

	// create a fresh CA bundle
	apiService.Spec.CABundle = caPEM

	// attempt a update or create
	if exists {
		logger.Debug("updating APIService")
		_, err = a.OpClient.UpdateAPIService(apiService)
	} else {
		logger.Debug("creating APIService")
		_, err = a.OpClient.CreateAPIService(apiService)
	}

	if err != nil {
		logger.Warnf("could not create or update APIService")
		return nil, err
	}

	return &depSpec, nil
}

// APIServiceNameToServiceName returns the result of replacing all
// periods in the given APIService name with hyphens
func APIServiceNameToServiceName(apiServiceName string) string {
	// Replace all '.'s with "-"s to convert to a DNS-1035 label
	return strings.Replace(apiServiceName, ".", "-", -1)
}

// safeToReplace ensures that the given set of OwnerReferences refers to CSVs in the given CSV's replacement chain
// or the CSV itself
func (a *Operator) safeToReplace(csv *v1alpha1.ClusterServiceVersion, owners []metav1.OwnerReference) bool {
	replaces := map[string]struct{}{
		csv.GetName(): {},
	}
	for _, r := range a.findIntermediatesForDeletion(csv) {
		replaces[r.GetName()] = struct{}{}
	}

	for _, owner := range owners {
		if owner.Kind != v1alpha1.ClusterServiceVersionKind {
			return false
		}

		if _, ok := replaces[owner.Name]; !ok {
			return false
		}
	}

	return true
}
