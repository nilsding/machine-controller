/*
Copyright 2019 The Machine Controller Authors.

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

package flatcar

import (
	"encoding/json"

	"github.com/kubermatic/machine-controller/pkg/providerconfig/types"

	"k8s.io/apimachinery/pkg/runtime"
)

// ProvisioningUtility specifies the type of provisioning utility.
type ProvisioningUtility string

const (
	Ignition  ProvisioningUtility = "ignition"
	CloudInit ProvisioningUtility = "cloud-init"
)

// Config contains specific configuration for Flatcar.
type Config struct {
	DisableAutoUpdate   bool `json:"disableAutoUpdate"`
	DisableLocksmithD   bool `json:"disableLocksmithD"`
	DisableUpdateEngine bool `json:"disableUpdateEngine"`

	// ProvisioningUtility specifies the type of provisioning utility, allowed values are cloud-init and ignition.
	// Defaults to ignition.
	ProvisioningUtility `json:"provisioningUtility,omitempty"`
}

func DefaultConfig(operatingSystemSpec runtime.RawExtension) runtime.RawExtension {
	return DefaultConfigForCloud(operatingSystemSpec, "")
}

func DefaultConfigForCloud(operatingSystemSpec runtime.RawExtension, cloudProvider types.CloudProvider) runtime.RawExtension {
	osSpec := Config{}
	if cloudProvider == types.CloudProviderAWS {
		osSpec.ProvisioningUtility = CloudInit
	}

	if operatingSystemSpec.Raw == nil {
		operatingSystemSpec.Raw, _ = json.Marshal(osSpec)
	}

	return operatingSystemSpec
}

// LoadConfig retrieves the Flatcar configuration from raw data.
func LoadConfig(r runtime.RawExtension) (*Config, error) {
	r = DefaultConfig(r)
	cfg := Config{}

	if err := json.Unmarshal(r.Raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Spec return the configuration as raw data.
func (cfg *Config) Spec() (*runtime.RawExtension, error) {
	ext := &runtime.RawExtension{}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	ext.Raw = b
	return ext, nil
}
