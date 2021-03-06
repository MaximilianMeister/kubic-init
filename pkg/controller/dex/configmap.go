/*
Copyright 2018 SUSE LINUX GmbH, Nuernberg, Germany..

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//go:generate ../../../build/asset.sh --var configMapTemplate --package dex --in dex-config.yaml.in --out configmap

package dex

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"

	"github.com/golang/glog"
	"github.com/kubernetes/kubernetes/cmd/kubeadm/app/util/apiclient"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kuberuntime "k8s.io/apimachinery/pkg/runtime"
	clientsetscheme "k8s.io/client-go/kubernetes/scheme"

	kubicv1beta1 "github.com/kubic-project/kubic-init/pkg/apis/kubic/v1beta1"
	kubiccfg "github.com/kubic-project/kubic-init/pkg/config"
	"github.com/kubic-project/kubic-init/pkg/crypto"
	"github.com/kubic-project/kubic-init/pkg/util"
)

const (
	dexConfigMapName = "kubic-dex"

	dexConfigMapFilename = "config.yaml"

	// default Dex port
	dexDefaultPort = 32000
)

type DexConfigMap struct {
	kubicCfg *kubiccfg.KubicInitConfiguration
	instance *kubicv1beta1.DexConfiguration

	FileName string

	current    *corev1.ConfigMap
	generated  *corev1.ConfigMap
	reconciler *ReconcileDexConfiguration
}

func NewDexConfigMapFor(instance *kubicv1beta1.DexConfiguration, reconciler *ReconcileDexConfiguration) (*DexConfigMap, error) {
	cm := &DexConfigMap{
		reconciler.kubicCfg,
		instance,
		dexConfigMapFilename,
		nil,
		nil,
		reconciler,
	}

	if err := cm.GetFrom(instance); err != nil {
		return nil, err
	}
	return cm, nil
}

// GetFrom obtains the current configmap fromm the ConfigMap specified in the instance.Status
func (config *DexConfigMap) GetFrom(instance *kubicv1beta1.DexConfiguration) error {
	var err error
	var name, namespace string

	// Try to the get current ConfigMap from the data in the instance.Status.Deployment
	if len(instance.Status.Config) > 0 {
		nname := util.StringToNamespacedName(instance.Status.Config)
		name, namespace = nname.Name, nname.Namespace
	} else {
		name, namespace = config.GetName(), config.GetNamespace()
	}

	// try to get the current ConfigMap
	config.current, err = config.reconciler.Clientset.Core().ConfigMaps(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		config.current = nil
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		glog.V(3).Infof("[kubic] there is an existing ConfigMap for Dex")
	}

	return nil
}

// CreateLocal generates a local ConfigMap instance. Note well that this instance is
// not published to the apiserver: users must use `CreateOrUpdate()` for doing that.
func (config *DexConfigMap) CreateLocal(connectors []kubicv1beta1.LDAPConnector,
	sharedPasswords crypto.SharedPasswordsSet) error {

	var err error

	glog.V(3).Infoln("[kubic] generating local ConfigMap for Dex")

	// get a valid address for the "issuer" in the Dex configuration
	dexAddress, err := config.kubicCfg.GetPublicAPIAddress()
	if err != nil {
		return err
	}

	port := dexDefaultPort
	if config.instance.Spec.NodePort != 0 {
		port = config.instance.Spec.NodePort
	}

	replacements := struct {
		KubicCfg           *kubiccfg.KubicInitConfiguration
		Config             *DexConfigMap
		DexName            string
		DexNamespace       string
		DexAddress         string
		DexPort            int
		DexSharedPasswords crypto.SharedPasswordsSet
		DexCertsDir        string
		LDAPConnectors     []kubicv1beta1.LDAPConnector
	}{
		config.kubicCfg,
		config,
		config.GetName(),
		config.GetNamespace(),
		dexAddress,
		port,
		sharedPasswords,
		dexCertsDir,
		connectors,
	}

	configMapBytes, err := util.ParseTemplate(configMapTemplate, replacements)
	if err != nil {
		return fmt.Errorf("error when parsing Dex configmap template: %v", err)
	}
	glog.V(8).Infof("[kubic] ConfigMap for Dex:\n%s\n", configMapBytes)

	config.generated = &corev1.ConfigMap{}
	if err := kuberuntime.DecodeInto(clientsetscheme.Codecs.UniversalDecoder(), []byte(configMapBytes), config.generated); err != nil {
		glog.V(3).Infof("[kubic] ConfigMap decoding error: %s", err)
		return fmt.Errorf("unable to decode dex configmap %v", err)
	}
	return nil
}

// NeedsCreateOrUpdate returns true if the ConfigMap is not in the cluster or it needs to be updated
// CreateLocal() must have been previously
func (config DexConfigMap) NeedsCreateOrUpdate() bool {
	if config.generated == nil {
		panic("ConfigMap has not been generated")
	}
	if config.current == nil {
		return true
	}
	return !reflect.DeepEqual(config.generated.Data, config.current.Data)
}

// CreateOrUpdate creates the ConfigMap in the apiserver, or updates an existing instance
func (config *DexConfigMap) CreateOrUpdate() error {
	var err error

	if config.generated == nil {
		// this would be an error in our program's logic
		panic("ConfigMap has not been generated")
	}

	// Create the ConfigMap for Dex or update it in case it already exists
	glog.V(3).Infof("[kubic] creating/updating ConfigMap '%s'", util.NamespacedObjToString(config))
	err = apiclient.CreateOrUpdateConfigMap(config.reconciler.Clientset, config.generated)
	if err != nil {
		glog.V(3).Infof("[kubic] could not create/update ConfigMap '%s': %s", util.NamespacedObjToString(config), err)
		return err
	}

	glog.V(5).Infof("[kubic] ConfigMap '%s' successfully created: refreshing local copy.", util.NamespacedObjToString(config))
	config.current, err = config.reconciler.Clientset.Core().ConfigMaps(config.GetNamespace()).Get(config.GetName(), metav1.GetOptions{})
	if err != nil {
		glog.V(3).Infof("[kubic] could not create/update ConfigMap '%s': %s", util.NamespacedObjToString(config), err)
		config.current = nil
		return err
	}

	return nil
}

// GetHashGenerated returns the hash of the generated config map
func (config DexConfigMap) GetHashGenerated() string {
	data := config.generated.BinaryData

	// calculate the sha256 of the configmap
	return fmt.Sprintf("%x", sha256.Sum256(data[dexConfigMapFilename]))
}

// Delete removes the current ConfigMap
func (config *DexConfigMap) Delete() error {
	if config.current != nil {
		if err := config.reconciler.Delete(context.TODO(), config.current); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		config.current = nil
	}

	return nil
}

func (config *DexConfigMap) GetObject() metav1.Object {
	if config.generated == nil {
		panic("needs to be generated first")
	}
	return config.generated
}

func (config DexConfigMap) GetName() string {
	return dexConfigMapName
}

func (config DexConfigMap) GetNamespace() string {
	return dexDefaultNamespace
}

func (config DexConfigMap) String() string {
	return util.NamespacedObjToString(config)
}
