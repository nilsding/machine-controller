/*
Copyright 2022 The Machine Controller Authors.

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

package opennebula

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/OpenNebula/one/src/oca/go/src/goca"
	"github.com/OpenNebula/one/src/oca/go/src/goca/schemas/shared"
	"github.com/OpenNebula/one/src/oca/go/src/goca/schemas/vm"
	"github.com/OpenNebula/one/src/oca/go/src/goca/schemas/vm/keys"

	"github.com/kubermatic/machine-controller/pkg/apis/cluster/common"
	clusterv1alpha1 "github.com/kubermatic/machine-controller/pkg/apis/cluster/v1alpha1"
	cloudprovidererrors "github.com/kubermatic/machine-controller/pkg/cloudprovider/errors"
	"github.com/kubermatic/machine-controller/pkg/cloudprovider/instance"
	opennebulatypes "github.com/kubermatic/machine-controller/pkg/cloudprovider/provider/opennebula/types"
	cloudprovidertypes "github.com/kubermatic/machine-controller/pkg/cloudprovider/types"
	"github.com/kubermatic/machine-controller/pkg/providerconfig"
	providerconfigtypes "github.com/kubermatic/machine-controller/pkg/providerconfig/types"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type provider struct {
	configVarResolver *providerconfig.ConfigVarResolver
}

type CloudProviderSpec struct {
	PassValidation bool `json:"passValidation"`
}

const (
	machineUidContextKey = "K8S_MACHINE_UID"
)

// New returns a OpenNebula provider.
func New(configVarResolver *providerconfig.ConfigVarResolver) cloudprovidertypes.Provider {
	return &provider{configVarResolver: configVarResolver}
}

type Config struct {
	// Auth details
	Username string
	Password string
	Endpoint string

	// Machine details
	Cpu       *float64
	Vcpu      *int
	Memory    *int
	Image     string
	Datastore string
	DiskSize  *int
	Network   string
	EnableVNC bool
}

func getClient(config *Config) *goca.Client {
	return goca.NewDefaultClient(goca.NewConfig(config.Username, config.Password, config.Endpoint))
}

func (p *provider) getConfig(provSpec clusterv1alpha1.ProviderSpec) (*Config, *providerconfigtypes.Config, error) {
	if provSpec.Value == nil {
		return nil, nil, fmt.Errorf("machine.spec.providerconfig.value is nil")
	}

	pconfig, err := providerconfigtypes.GetConfig(provSpec)
	if err != nil {
		return nil, nil, err
	}

	rawConfig, err := opennebulatypes.GetConfig(*pconfig)
	if err != nil {
		return nil, nil, err
	}

	c := Config{}
	c.Username, err = p.configVarResolver.GetConfigVarStringValueOrEnv(rawConfig.Username, "ONE_USERNAME")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get the value of \"username\" field, error = %w", err)
	}

	c.Password, err = p.configVarResolver.GetConfigVarStringValueOrEnv(rawConfig.Password, "ONE_PASSWORD")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get the value of \"password\" field, error = %w", err)
	}

	c.Endpoint, err = p.configVarResolver.GetConfigVarStringValueOrEnv(rawConfig.Endpoint, "ONE_ENDPOINT")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get the value of \"endpoint\" field, error = %w", err)
	}

	c.Cpu = rawConfig.Cpu

	c.Vcpu = rawConfig.Vcpu

	c.Memory = rawConfig.Memory

	c.Image, err = p.configVarResolver.GetConfigVarStringValue(rawConfig.Image)
	if err != nil {
		return nil, nil, err
	}

	c.Datastore, err = p.configVarResolver.GetConfigVarStringValue(rawConfig.Datastore)
	if err != nil {
		return nil, nil, err
	}

	c.DiskSize = rawConfig.DiskSize

	c.Network, err = p.configVarResolver.GetConfigVarStringValue(rawConfig.Network)
	if err != nil {
		return nil, nil, err
	}

	c.EnableVNC, _, err = p.configVarResolver.GetConfigVarBoolValue(rawConfig.EnableVNC)
	if err != nil {
		return nil, nil, err
	}

	return &c, pconfig, err
}

func (p *provider) Validate(ctx context.Context, spec clusterv1alpha1.MachineSpec) error {
	_, pc, err := p.getConfig(spec.ProviderSpec)
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	opennebulaCloudProviderSpec := CloudProviderSpec{}
	if err = json.Unmarshal(pc.CloudProviderSpec.Raw, &opennebulaCloudProviderSpec); err != nil {
		return err
	}

	return nil
}

func (p *provider) GetCloudConfig(_ clusterv1alpha1.MachineSpec) (string, string, error) {
	return "", "", nil
}

func (p *provider) Create(ctx context.Context, machine *clusterv1alpha1.Machine, data *cloudprovidertypes.ProviderData, userdata string) (instance.Instance, error) {
	c, _, err := p.getConfig(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, cloudprovidererrors.TerminalError{
			Reason:  common.InvalidConfigurationMachineError,
			Message: fmt.Sprintf("Failed to parse MachineSpec, due to %v", err),
		}
	}

	client := getClient(c)

	// build a template
	tpl := vm.NewTemplate()
	tpl.Add(keys.Name, machine.Spec.Name)
	tpl.CPU(*c.Cpu).Memory(*c.Memory).VCPU(*c.Vcpu)

	disk := tpl.AddDisk()
	disk.Add(shared.Image, c.Image)
	disk.Add(shared.Datastore, c.Datastore)
	disk.Add(shared.DevPrefix, "vd")
	disk.Add(shared.Size, c.DiskSize)

	nic := tpl.AddNIC()
	nic.Add(shared.Network, c.Network)
	nic.Add(shared.Model, "virtio")

	if c.EnableVNC {
		tpl.AddIOGraphic(keys.GraphicType, "VNC")
		tpl.AddIOGraphic(keys.Listen, "0.0.0.0")
	}

	tpl.AddCtx(keys.NetworkCtx, "YES")
	tpl.AddCtx(keys.SSHPubKey, "$USER[SSH_PUBLIC_KEY]")

	tpl.AddCtx(machineUidContextKey, string(machine.UID))
	tpl.AddCtx("USER_DATA", userdata)

	controller := goca.NewController(client)

	// create VM from the generated template above
	vmID, err := controller.VMs().Create(tpl.String(), false)
	if err != nil {
		return nil, err
	}

	vm, err := controller.VM(vmID).Info(false)
	if err != nil {
		return nil, err
	}

	return &openNebulaInstance{vm}, nil
}

func (p *provider) Cleanup(ctx context.Context, machine *clusterv1alpha1.Machine, data *cloudprovidertypes.ProviderData) (bool, error) {
	instance, err := p.Get(ctx, machine, data)
	if err != nil {
		if errors.Is(err, cloudprovidererrors.ErrInstanceNotFound) {
			return true, nil
		}
		return false, err
	}

	c, _, err := p.getConfig(machine.Spec.ProviderSpec)
	if err != nil {
		return false, cloudprovidererrors.TerminalError{
			Reason:  common.InvalidConfigurationMachineError,
			Message: fmt.Sprintf("Failed to parse MachineSpec, due to %v", err),
		}
	}

	client := getClient(c)
	controller := goca.NewController(client)

	vmctrl := controller.VM(instance.(*openNebulaInstance).vm.ID)
	err = vmctrl.TerminateHard()
	// ignore error of nonexistent machines by matching for "NO_EXISTS", the error string is something like "OpenNebula error [NO_EXISTS]: [one.vm.action] Error getting virtual machine [999914743]."
	if err != nil && !strings.Contains(err.Error(), "NO_EXISTS") {
		return false, cloudprovidererrors.TerminalError{
			Reason:  common.InvalidConfigurationMachineError,
			Message: fmt.Sprintf("failed to delete virtual machine, due to %v", err),
		}
	}

	return true, nil
}

func (p *provider) Get(_ context.Context, machine *clusterv1alpha1.Machine, _ *cloudprovidertypes.ProviderData) (instance.Instance, error) {
	c, _, err := p.getConfig(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, cloudprovidererrors.TerminalError{
			Reason:  common.InvalidConfigurationMachineError,
			Message: fmt.Sprintf("Failed to parse MachineSpec, due to %v", err),
		}
	}

	client := getClient(c)
	controller := goca.NewController(client)

	vmPool, err := controller.VMs().Info()
	if err != nil {
		return nil, cloudprovidererrors.TerminalError{
			Reason:  common.InvalidConfigurationMachineError,
			Message: fmt.Sprintf("failed to list virtual machines, due to %v", err),
		}
	}

	// first collect all IDs, the vm infos in the vmPool don't contain the context which has the uid
	var vmIDs []int
	for _, vm := range vmPool.VMs {
		if vm.Name != machine.Spec.Name {
			continue
		}

		vmIDs = append(vmIDs, vm.ID)
	}

	// go over each vm that matches the name and check if the uid is the same
	for _, vmID := range vmIDs {
		vm, err := controller.VM(vmID).Info(false)
		if err != nil {
			return nil, cloudprovidererrors.TerminalError{
				Reason:  common.InvalidConfigurationMachineError,
				Message: fmt.Sprintf("failed to get info for VM %v, due to %v", vmID, err),
			}
		}

		uid, err := vm.Template.GetCtx(machineUidContextKey)
		if err != nil {
			// ignore errors like "key blabla not found"
			continue
		}

		if uid == string(machine.UID) {
			return &openNebulaInstance{vm}, nil
		}
	}

	return nil, cloudprovidererrors.ErrInstanceNotFound
}

func (p *provider) AddDefaults(spec clusterv1alpha1.MachineSpec) (clusterv1alpha1.MachineSpec, error) {
	return spec, nil
}

func (p *provider) MigrateUID(ctx context.Context, machine *clusterv1alpha1.Machine, newUID types.UID) error {
	// TODO: implement this
	return nil
}

func (p *provider) MachineMetricsLabels(_ *clusterv1alpha1.Machine) (map[string]string, error) {
	return map[string]string{}, nil
}

func (p *provider) SetMetricsForMachines(_ clusterv1alpha1.MachineList) error {
	return nil
}

type openNebulaInstance struct {
	vm *vm.VM
}

func (i *openNebulaInstance) Name() string {
	return i.vm.Name
}

func (i *openNebulaInstance) ID() string {
	return strconv.Itoa(i.vm.ID)
}

func (i *openNebulaInstance) ProviderID() string {
	// ??? where does this get used?
	return "opennebula://" + strconv.Itoa(i.vm.ID)
}

func (i *openNebulaInstance) Addresses() map[string]v1.NodeAddressType {
	addresses := map[string]v1.NodeAddressType{}

	for _, nic := range i.vm.Template.GetNICs() {
		ip, _ := nic.Get(shared.IP)
		addresses[ip] = v1.NodeExternalIP
	}

	return nil
}

func (i *openNebulaInstance) Status() instance.Status {
	// state is the general state of the VM, lcmState is the state of the life-cycle manager of the VM
	// lcmState is anything else other than LcmInit when the VM's state is Active
	state, lcmState, _ := i.vm.State()
	switch state {
	case vm.Init, vm.Pending, vm.Hold:
		return instance.StatusCreating
	case vm.Active:
		switch lcmState {
		case vm.LcmInit, vm.Prolog, vm.Boot:
			return instance.StatusCreating
		case vm.Epilog:
			return instance.StatusDeleting
		default:
			return instance.StatusRunning
		}
	case vm.Done:
		return instance.StatusDeleted
	default:
		return instance.StatusUnknown
	}
}
