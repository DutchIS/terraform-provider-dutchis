package dutchis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// using a global variable here so that we have an internally accessible
// way to look into our own resource definition. Useful for dynamically doing typecasts
// so that we can print (debug) our ResourceData constructs
var thisResource *schema.Resource

func resourceVmQemu() *schema.Resource {
	thisResource = &schema.Resource{
		Create:        resourceVmQemuCreate,
		Read:          resourceVmQemuRead,
		UpdateContext: resourceVmQemuUpdate,
		Delete:        resourceVmQemuDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema {
			"hostname": {
				Type:     schema.TypeString,
				Optional: false,
				Description: "The virtual server hostname",
			},
			"class": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The Performance class of the virtual server",
			},
			"os": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "OS id of the virtual server",
			},
			"username": {
				Type:        schema.TypeString,
				Required:    false,
				Description: "The username of the virtual server. This is ignored on Windows servers",
			},
			"password": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The password of the default user",
			},
			"sshkeys": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Provide the UUID's of ssh keys or provide a ssh key in openssh format.",
			},
			"cores": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The amount of cores to assign to the virtual server",
			},
			"memory": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The amount of memory in GB to assign to the virtual server",
			},
			"network": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The network speed in Gbps for this virtual server",
			},
			"disk": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The amount of storage space in GB to assign to the virtual server",
			},
		},
		Timeouts: resourceTimeouts(),
	}
	return thisResource
}

func resourceVmQemuCreate(d *schema.ResourceData, meta interface{}) error {
	// create a logger for this function
	logger, _ := CreateSubLogger("resource_vm_create")

	// DEBUG print out the create request
	flatValue, _ := resourceDataToFlatValues(d, thisResource)
	jsonString, _ := json.Marshal(flatValue)
	logger.Info().Str("vmid", d.Id()).Msgf("VM creation")
	logger.Debug().Str("vmid", d.Id()).Msgf("VM creation resource data: '%+v'", string(jsonString))

	pconf := meta.(*providerConfiguration)
	lock := pmParallelBegin(pconf)
	// defer lock.unlock()
	client := pconf.Client
	vmName := d.Get("name").(string)
	vga := d.Get("vga").(*schema.Set)
	qemuVgaList := vga.List()

	qemuNetworks, _ := ExpandDevicesList(d.Get("network").([]interface{}))
	qemuDisks, _ := ExpandDevicesList(d.Get("disk").([]interface{}))

	serials := d.Get("serial").(*schema.Set)
	qemuSerials, _ := DevicesSetToMap(serials)

	qemuPCIDevices, _ := ExpandDevicesList(d.Get("hostpci").([]interface{}))

	qemuUsbs, _ := ExpandDevicesList(d.Get("usb").([]interface{}))

	config := pxapi.ConfigQemu{
		Name:           vmName,
		Description:    d.Get("desc").(string),
		Pool:           d.Get("pool").(string),
		Bios:           d.Get("bios").(string),
		Onboot:         BoolPointer(d.Get("onboot").(bool)),
		Startup:        d.Get("startup").(string),
		Tablet:         BoolPointer(d.Get("tablet").(bool)),
		Boot:           d.Get("boot").(string),
		BootDisk:       d.Get("bootdisk").(string),
		Agent:          d.Get("agent").(int),
		Memory:         d.Get("memory").(int),
		Machine:        d.Get("machine").(string),
		Balloon:        d.Get("balloon").(int),
		QemuCores:      d.Get("cores").(int),
		QemuSockets:    d.Get("sockets").(int),
		QemuVcpus:      d.Get("vcpus").(int),
		QemuCpu:        d.Get("cpu").(string),
		QemuNuma:       BoolPointer(d.Get("numa").(bool)),
		QemuKVM:        BoolPointer(d.Get("kvm").(bool)),
		Hotplug:        d.Get("hotplug").(string),
		Scsihw:         d.Get("scsihw").(string),
		HaState:        d.Get("hastate").(string),
		HaGroup:        d.Get("hagroup").(string),
		QemuOs:         d.Get("qemu_os").(string),
		Tags:           d.Get("tags").(string),
		Args:           d.Get("args").(string),
		QemuNetworks:   qemuNetworks,
		QemuDisks:      qemuDisks,
		QemuSerials:    qemuSerials,
		QemuPCIDevices: qemuPCIDevices,
		QemuUsbs:       qemuUsbs,
		Smbios1:        BuildSmbiosArgs(d.Get("smbios").([]interface{})),
		// Cloud-init.
		CIuser:       d.Get("ciuser").(string),
		CIpassword:   d.Get("cipassword").(string),
		CIcustom:     d.Get("cicustom").(string),
		Searchdomain: d.Get("searchdomain").(string),
		Nameserver:   d.Get("nameserver").(string),
		Sshkeys:      d.Get("sshkeys").(string),
		Ipconfig:     pxapi.IpconfigMap{},
	}
	// Populate Ipconfig map
	for i := 0; i < 16; i++ {
		iface := fmt.Sprintf("ipconfig%d", i)
		if v, ok := d.GetOk(iface); ok {
			config.Ipconfig[i] = v.(string)
		}
	}
	if len(qemuVgaList) > 0 {
		config.QemuVga = qemuVgaList[0].(map[string]interface{})
	}
	log.Printf("[DEBUG][QemuVmCreate] checking for duplicate name: %s", vmName)
	dupVmr, _ := client.GetVmRefByName(vmName)

	forceCreate := d.Get("force_create").(bool)
	targetNode := d.Get("target_node").(string)
	pool := d.Get("pool").(string)

	if dupVmr != nil && forceCreate {
		return fmt.Errorf("duplicate VM name (%s) with vmId: %d. Set force_create=false to recycle", vmName, dupVmr.VmId())
	} else if dupVmr != nil && dupVmr.Node() != targetNode {
		return fmt.Errorf("duplicate VM name (%s) with vmId: %d on different target_node=%s", vmName, dupVmr.VmId(), dupVmr.Node())
	}

	vmr := dupVmr

	if vmr == nil {
		// get unique id
		nextid, err := nextVmId(pconf)
		vmID := d.Get("vmid").(int)
		if vmID != 0 { // 0 is the "no value" for int in golang
			nextid = vmID
		} else {
			if err != nil {
				return err
			}
		}

		vmr = pxapi.NewVmRef(nextid)
		vmr.SetNode(targetNode)
		if pool != "" {
			vmr.SetPool(pool)
		}

		// check if ISO, clone, or PXE boot
		if d.Get("clone").(string) != "" {
			fullClone := 1
			if !d.Get("full_clone").(bool) {
				fullClone = 0
			}
			config.FullClone = &fullClone

			sourceVmrs, err := client.GetVmRefsByName(d.Get("clone").(string))
			if err != nil {
				return err
			}

			// prefer source Vm located on same node
			sourceVmr := sourceVmrs[0]
			for _, candVmr := range sourceVmrs {
				if candVmr.Node() == vmr.Node() {
					sourceVmr = candVmr
				}
			}

			log.Print("[DEBUG][QemuVmCreate] cloning VM")
			logger.Debug().Str("vmid", d.Id()).Msgf("Cloning VM")
			err = config.CloneVm(sourceVmr, vmr, client)
			if err != nil {
				return err
			}
			// give sometime to proxmox to catchup
			time.Sleep(time.Duration(d.Get("clone_wait").(int)) * time.Second)

			config_post_clone, err := pxapi.NewConfigQemuFromApi(vmr, client)
			if err != nil {
				return err
			}

			logger.Debug().Str("vmid", d.Id()).Msgf("Original disks: '%+v', Clone Disks '%+v'", config.QemuDisks, config_post_clone.QemuDisks)

			// update the current working state to use the appropriate file specification
			// proxmox needs so we can correctly update the existing disks (post-clone)
			// instead of accidentially causing the existing disk to be detached.
			// see https://github.com/Telmate/terraform-provider-proxmox/issues/239
			for slot, disk := range config_post_clone.QemuDisks {
				// only update the desired configuration if it was not set by the user
				// we do not want to overwrite the desired config with the results from
				// proxmox if the user indicates they wish a particular file or volume config
				if config.QemuDisks[slot]["file"] == "" {
					config.QemuDisks[slot]["file"] = disk["file"]
				}
				if config.QemuDisks[slot]["volume"] == "" {
					config.QemuDisks[slot]["volume"] = disk["volume"]
				}
			}

			err = config.UpdateConfig(vmr, client)
			if err != nil {
				// Set the id because when update config fail the vm is still created
				d.SetId(resourceId(targetNode, "qemu", vmr.VmId()))
				return err
			}

			err = prepareDiskSize(client, vmr, qemuDisks, d)
			if err != nil {
				d.SetId(resourceId(targetNode, "qemu", vmr.VmId()))
				return err
			}

		} else if d.Get("iso").(string) != "" {
			config.QemuIso = d.Get("iso").(string)
			err := config.CreateVm(vmr, client)
			if err != nil {
				return err
			}
		} else if d.Get("pxe").(bool) {
			var found bool
			bs := d.Get("boot").(string)
			// This used to be multiple regexes. Keeping the loop for flexibility.
			regs := [...]string{"^order=.*net.*$"}

			for _, reg := range regs {
				re, err := regexp.Compile(reg)
				if err != nil {
					return err
				}

				found = re.MatchString(bs)

				if found {
					break
				}
			}

			if !found {
				return fmt.Errorf("no network boot option matched in 'boot' config")
			}

			err := config.CreateVm(vmr, client)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("either 'clone', 'iso', or 'pxe' must be set")
		}
	} else {
		log.Printf("[DEBUG][QemuVmCreate] recycling VM vmId: %d", vmr.VmId())

		client.StopVm(vmr)

		err := config.UpdateConfig(vmr, client)
		if err != nil {
			// Set the id because when update config fail the vm is still created
			d.SetId(resourceId(targetNode, "qemu", vmr.VmId()))
			return err
		}

		// give sometime to proxmox to catchup
		// time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)

		err = prepareDiskSize(client, vmr, qemuDisks, d)
		if err != nil {
			return err
		}
	}
	d.SetId(resourceId(targetNode, "qemu", vmr.VmId()))
	logger.Debug().Int("vmid", vmr.VmId()).Msgf("Set this vm (resource Id) to '%v'", d.Id())

	if d.Get("cloudinit_cdrom_storage").(string) != "" {
		vmParams := map[string]interface{}{
			"cdrom": fmt.Sprintf("%s:cloudinit", d.Get("cloudinit_cdrom_storage").(string)),
		}

		_, err := client.SetVmConfig(vmr, vmParams)
		if err != nil {
			return err
		}
	}

	// give sometime to proxmox to catchup
	time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)

	// TODO: remove "oncreate" handling in next major release.
	if d.Get("vm_state").(string) == "running" || d.Get("oncreate").(bool) {
		log.Print("[DEBUG][QemuVmCreate] starting VM")
		_, err := client.StartVm(vmr)
		if err != nil {
			return err
		}
		// give sometime to proxmox to catchup
		time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)

		err = initConnInfo(d, pconf, client, vmr, &config, lock)
		if err != nil {
			return err
		}
	} else {
		log.Print("[DEBUG][QemuVmCreate] vm_state != running, not starting VM")
	}

	// err := initConnInfo(d, pconf, client, vmr, &config, lock)
	// if err != nil {
	// 	return err
	// }
	log.Print("[DEBUG][QemuVmCreate] vm creation done!")
	lock.unlock()
	return resourceVmQemuRead(d, meta)
}

func resourceVmQemuUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	pconf := meta.(*providerConfiguration)
	client := pconf.Client
	lock := pmParallelBegin(pconf)

	// create a logger for this function
	logger, _ := CreateSubLogger("resource_vm_update")

	// get vmID
	_, _, vmID, err := parseResourceId(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	logger.Info().Int("vmid", vmID).Msg("Starting update of the VM resource")

	vmr := pxapi.NewVmRef(vmID)
	_, err = client.GetVmInfo(vmr)
	if err != nil {
		return diag.FromErr(err)
	}
	vga := d.Get("vga").(*schema.Set)
	qemuVgaList := vga.List()

	// okay, so the proxmox-api-go library is a bit weird about the updates. we can only send certain
	// parameters about the disk over otherwise a crash happens (if we send file), or it sends duplicate keys
	// to proxmox (if we send media). this is a bit hacky.. but it should paper over these issues until a more
	// robust solution can be found.
	qemuDisks, _ := ExpandDevicesList(d.Get("disk").([]interface{}))
	for _, diskParamMap := range qemuDisks {
		delete(diskParamMap, "file")  // removed; causes a crash in proxmox-api-go
		delete(diskParamMap, "media") // removed; results in a duplicate key issue causing a 400 from proxmox
	}

	qemuNetworks, err := ExpandDevicesList(d.Get("network").([]interface{}))
	if err != nil {
		return diag.FromErr(fmt.Errorf("error while processing Network configuration: %v", err))
	}
	logger.Debug().Int("vmid", vmID).Msgf("Processed NetworkSet into qemuNetworks as %+v", qemuNetworks)

	serials := d.Get("serial").(*schema.Set)
	qemuSerials, _ := DevicesSetToMap(serials)

	qemuPCIDevices, err := ExpandDevicesList(d.Get("hostpci").([]interface{}))
	if err != nil {
		return diag.FromErr(fmt.Errorf("error while processing HostPCI configuration: %v", err))
	}

	qemuUsbs, err := ExpandDevicesList(d.Get("usb").([]interface{}))
	if err != nil {
		return diag.FromErr(fmt.Errorf("error while processing Usb configuration: %v", err))
	}

	d.Partial(true)
	if d.HasChange("target_node") {
		_, err := client.MigrateNode(vmr, d.Get("target_node").(string), true)
		if err != nil {
			return diag.FromErr(err)
		}
		vmr.SetNode(d.Get("target_node").(string))
	}
	d.Partial(false)

	config := pxapi.ConfigQemu{
		Name:           d.Get("name").(string),
		Description:    d.Get("desc").(string),
		Pool:           d.Get("pool").(string),
		Bios:           d.Get("bios").(string),
		Onboot:         BoolPointer(d.Get("onboot").(bool)),
		Startup:        d.Get("startup").(string),
		Tablet:         BoolPointer(d.Get("tablet").(bool)),
		Boot:           d.Get("boot").(string),
		BootDisk:       d.Get("bootdisk").(string),
		Agent:          d.Get("agent").(int),
		Memory:         d.Get("memory").(int),
		Machine:        d.Get("machine").(string),
		Balloon:        d.Get("balloon").(int),
		QemuCores:      d.Get("cores").(int),
		QemuSockets:    d.Get("sockets").(int),
		QemuVcpus:      d.Get("vcpus").(int),
		QemuCpu:        d.Get("cpu").(string),
		QemuNuma:       BoolPointer(d.Get("numa").(bool)),
		QemuKVM:        BoolPointer(d.Get("kvm").(bool)),
		Hotplug:        d.Get("hotplug").(string),
		Scsihw:         d.Get("scsihw").(string),
		HaState:        d.Get("hastate").(string),
		HaGroup:        d.Get("hagroup").(string),
		QemuOs:         d.Get("qemu_os").(string),
		Tags:           d.Get("tags").(string),
		Args:           d.Get("args").(string),
		QemuNetworks:   qemuNetworks,
		QemuDisks:      qemuDisks,
		QemuSerials:    qemuSerials,
		QemuPCIDevices: qemuPCIDevices,
		QemuUsbs:       qemuUsbs,
		Smbios1:        BuildSmbiosArgs(d.Get("smbios").([]interface{})),
		// Cloud-init.
		CIuser:       d.Get("ciuser").(string),
		CIpassword:   d.Get("cipassword").(string),
		CIcustom:     d.Get("cicustom").(string),
		Searchdomain: d.Get("searchdomain").(string),
		Nameserver:   d.Get("nameserver").(string),
		Sshkeys:      d.Get("sshkeys").(string),
		Ipconfig: pxapi.IpconfigMap{
			0:  d.Get("ipconfig0").(string),
			1:  d.Get("ipconfig1").(string),
			2:  d.Get("ipconfig2").(string),
			3:  d.Get("ipconfig3").(string),
			4:  d.Get("ipconfig4").(string),
			5:  d.Get("ipconfig5").(string),
			6:  d.Get("ipconfig6").(string),
			7:  d.Get("ipconfig7").(string),
			8:  d.Get("ipconfig8").(string),
			9:  d.Get("ipconfig9").(string),
			10: d.Get("ipconfig10").(string),
			11: d.Get("ipconfig11").(string),
			12: d.Get("ipconfig12").(string),
			13: d.Get("ipconfig13").(string),
			14: d.Get("ipconfig14").(string),
			15: d.Get("ipconfig15").(string),
		},
	}
	if len(qemuVgaList) > 0 {
		config.QemuVga = qemuVgaList[0].(map[string]interface{})
	}

	logger.Debug().Int("vmid", vmID).Msgf("Updating VM with the following configuration: %+v", config)

	err = config.UpdateConfig(vmr, client)
	if err != nil {
		return diag.FromErr(err)
	}

	// give sometime to proxmox to catchup
	time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)

	prepareDiskSize(client, vmr, qemuDisks, d)

	// give sometime to proxmox to catchup
	time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)

	if d.HasChange("pool") {
		oldPool, newPool := func() (string, string) {
			a, b := d.GetChange("pool")
			return a.(string), b.(string)
		}()

		vmr := pxapi.NewVmRef(vmID)
		vmr.SetPool(oldPool)

		_, err := client.UpdateVMPool(vmr, newPool)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	err = initConnInfo(d, pconf, client, vmr, &config, lock)
	if err != nil {
		return diag.FromErr(err)
	}

	// If any of the "critical" keys are changed then a reboot is required.
	if d.HasChanges(
		"bios",
		"boot",
		"bootdisk",
		"agent",
		"qemu_os",
		"balloon",
		"cpu",
		"numa",
		"machine",
		"hotplug",
		"scsihw",
		"os_type",
		"ciuser",
		"cipassword",
		"cicustom",
		"searchdomain",
		"nameserver",
		"sshkeys",
		"ipconfig0",
		"ipconfig1",
		"ipconfig2",
		"ipconfig3",
		"ipconfig4",
		"ipconfig5",
		"ipconfig6",
		"ipconfig7",
		"ipconfig8",
		"ipconfig9",
		"ipconfig10",
		"ipconfig11",
		"ipconfig12",
		"ipconfig13",
		"ipconfig14",
		"ipconfig15",
		"kvm",
		"vga",
		"serial",
		"usb",
		"hostpci",
	) {
		d.Set("reboot_required", true)
	}

	// reboot is only required when memory hotplug is disabled
	if d.HasChange("memory") && !strings.Contains(d.Get("hotplug").(string), "memory") {
		d.Set("reboot_required", true)
	}

	// reboot is only required when cpu hotplug is disabled
	if d.HasChanges("sockets", "cores", "vcpus") && !strings.Contains(d.Get("hotplug").(string), "cpu") {
		d.Set("reboot_required", true)
	}

	// if network hot(un)plug is not enabled, then check if some of the "critical" parameters have changes
	if d.HasChange("network") && !strings.Contains(d.Get("hotplug").(string), "network") {
		oldValuesRaw, newValuesRaw := d.GetChange("network")
		oldValues := oldValuesRaw.([]interface{})
		newValues := newValuesRaw.([]interface{})
		if len(oldValues) != len(newValues) {
			// network interface added or removed
			d.Set("reboot_required", true)
		} else {
			// some of the existing interface parameters have changed
			for i := range oldValues { // loop through the interfaces
				if oldValues[i].(map[string]interface{})["model"] != newValues[i].(map[string]interface{})["model"] {
					d.Set("reboot_required", true)
				}
				if oldValues[i].(map[string]interface{})["macaddr"] != newValues[i].(map[string]interface{})["macaddr"] {
					d.Set("reboot_required", true)
				}
				if oldValues[i].(map[string]interface{})["queues"] != newValues[i].(map[string]interface{})["queues"] {
					d.Set("reboot_required", true)
				}
			}
		}
	}

	// some of the disk changes require reboot, even if hotplug is enabled
	if d.HasChange("disk") {
		oldValuesRaw, newValuesRaw := d.GetChange("disk")
		oldValues := oldValuesRaw.([]interface{})
		newValues := newValuesRaw.([]interface{})
		if len(oldValues) != len(newValues) && !strings.Contains(d.Get("hotplug").(string), "disk") {
			// disk added or removed AND there is no disk hot(un)plug
			d.Set("reboot_required", true)
		} else {
			r := len(oldValues)

			// we have have to check if the new configuration has fewer disks
			// otherwise an index out of range panic occurs if we don't reduce the range
			if rangeNV := len(newValues); rangeNV < r {
				r = rangeNV
			}

			// some of the existing disk parameters have changed
			for i := 0; i < r; i++ { // loop through the interfaces
				if oldValues[i].(map[string]interface{})["ssd"] != newValues[i].(map[string]interface{})["ssd"] {
					d.Set("reboot_required", true)
				}
				if oldValues[i].(map[string]interface{})["iothread"] != newValues[i].(map[string]interface{})["iothread"] {
					d.Set("reboot_required", true)
				}
				if oldValues[i].(map[string]interface{})["discard"] != newValues[i].(map[string]interface{})["discard"] {
					d.Set("reboot_required", true)
				}
				if oldValues[i].(map[string]interface{})["cache"] != newValues[i].(map[string]interface{})["cache"] {
					d.Set("reboot_required", true)
				}
				if oldValues[i].(map[string]interface{})["size"] != newValues[i].(map[string]interface{})["size"] {
					d.Set("reboot_required", true)
				}
				// these paramater changes only require reboot if disk hotplug is disabled
				if !strings.Contains(d.Get("hotplug").(string), "disk") {
					if oldValues[i].(map[string]interface{})["type"] != newValues[i].(map[string]interface{})["type"] {
						// note: changing type does not remove the old disk
						d.Set("reboot_required", true)
					}
				}
			}
		}
	}

	var diags diag.Diagnostics

	// Try rebooting the VM is a reboot is required and automatic_reboot is
	// enabled. Attempt a graceful shutdown or if that fails, force poweroff.
	vmState, err := client.GetVmState(vmr)
	if err == nil && vmState["status"] != "stopped" && d.Get("vm_state").(string) == "stopped" {
		log.Print("[DEBUG][QemuVmUpdate] shutting down VM to match `vm_state`")
		_, err = client.ShutdownVm(vmr)
		// note: the default timeout is 3 min, configurable per VM: Options/Start-Shutdown Order/Shutdown timeout
		if err != nil {
			log.Print("[DEBUG][QemuVmUpdate] shutdown failed, stopping VM forcefully")
			_, err = client.StopVm(vmr)
			if err != nil {
				return diag.FromErr(err)
			}
		}
	} else if err == nil && vmState["status"] != "stopped" && d.Get("reboot_required").(bool) {
		if d.Get("automatic_reboot").(bool) {
			log.Print("[DEBUG][QemuVmUpdate] shutting down VM for required reboot")
			_, err = client.ShutdownVm(vmr)
			// note: the default timeout is 3 min, configurable per VM: Options/Start-Shutdown Order/Shutdown timeout
			if err != nil {
				log.Print("[DEBUG][QemuVmUpdate] shutdown failed, stopping VM forcefully")
				_, err = client.StopVm(vmr)
				if err != nil {
					return diag.FromErr(err)
				}
			}
		} else {
			// Automatic reboots is not enabled, show the user a warning message that
			// the VM needs a reboot for the changed parameters to take in effect.
			diags = append(diags, diag.Diagnostic{
				Severity:      diag.Warning,
				Summary:       "VM needs to be rebooted and automatic_reboot is disabled",
				Detail:        "One or more parameters are modified that only take effect after a reboot (shutdown & start).",
				AttributePath: cty.Path{},
			})
		}
	} else if err != nil {
		return diag.FromErr(err)
	}

	// Start VM only if it isn't running and it should be.
	vmState, err = client.GetVmState(vmr)
	if err == nil && vmState["status"] == "stopped" && d.Get("vm_state").(string) == "running" {
		log.Print("[DEBUG][QemuVmUpdate] starting VM")
		_, err = client.StartVm(vmr)
		if err != nil {
			return diag.FromErr(err)
		}
	} else if err != nil {
		diags = append(diags, diag.FromErr(err)...)
		return diags
	}
	lock.unlock()

	err = resourceVmQemuRead(d, meta)
	if err != nil {
		diags = append(diags, diag.FromErr(err)...)
		return diags
	}

	return diags
}

func resourceVmQemuRead(d *schema.ResourceData, meta interface{}) error {
	return _resourceVmQemuRead(d, meta)
}

func _resourceVmQemuRead(d *schema.ResourceData, meta interface{}) error {
	pconf := meta.(*providerConfiguration)
	lock := pmParallelBegin(pconf)
	defer lock.unlock()
	client := pconf.Client
	// create a logger for this function
	logger, _ := CreateSubLogger("resource_vm_read")

	_, _, vmID, err := parseResourceId(d.Id())
	if err != nil {
		d.SetId("")
		return fmt.Errorf("unexpected error when trying to read and parse the resource: %v", err)
	}

	logger.Info().Int("vmid", vmID).Msg("Reading configuration for vmid")
	vmr := pxapi.NewVmRef(vmID)

	// Try to get information on the vm. If this call err's out
	// that indicates the VM does not exist. We indicate that to terraform
	// by calling a SetId("")
	_, err = client.GetVmInfo(vmr)
	if err != nil {
		logger.Debug().Int("vmid", vmID).Err(err).Msg("failed to get vm info")
		d.SetId("")
		return nil
	}
	config, err := pxapi.NewConfigQemuFromApi(vmr, client)
	if err != nil {
		return err
	}

	vmState, err := client.GetVmState(vmr)
	log.Printf("[DEBUG] VM status: %s", vmState["status"])
	if err == nil {
		d.Set("vm_state", vmState["status"])
	}
	if err == nil && vmState["status"] == "started" {
		log.Printf("[DEBUG] VM is running, cheking the IP")
		err = initConnInfo(d, pconf, client, vmr, config, lock)
		if err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}

	logger.Debug().Int("vmid", vmID).Msgf("[READ] Received Config from Proxmox API: %+v", config)

	d.SetId(resourceId(vmr.Node(), "qemu", vmr.VmId()))
	d.Set("target_node", vmr.Node())
	d.Set("name", config.Name)
	d.Set("desc", config.Description)
	d.Set("bios", config.Bios)
	d.Set("onboot", config.Onboot)
	d.Set("startup", config.Startup)
	d.Set("tablet", config.Tablet)
	d.Set("boot", config.Boot)
	d.Set("bootdisk", config.BootDisk)
	d.Set("agent", config.Agent)
	d.Set("memory", config.Memory)
	d.Set("machine", config.Machine)
	d.Set("balloon", config.Balloon)
	d.Set("cores", config.QemuCores)
	d.Set("sockets", config.QemuSockets)
	d.Set("vcpus", config.QemuVcpus)
	d.Set("cpu", config.QemuCpu)
	d.Set("numa", config.QemuNuma)
	d.Set("kvm", config.QemuKVM)
	d.Set("hotplug", config.Hotplug)
	d.Set("scsihw", config.Scsihw)
	d.Set("hastate", vmr.HaState())
	d.Set("hagroup", vmr.HaGroup())
	d.Set("qemu_os", config.QemuOs)
	d.Set("tags", config.Tags)
	d.Set("args", config.Args)
	// Cloud-init.
	d.Set("ciuser", config.CIuser)
	// we purposely use the password from the terraform config here
	// because the proxmox api will always return "**********" leading to diff issues
	d.Set("cipassword", d.Get("cipassword").(string))
	d.Set("cicustom", config.CIcustom)
	d.Set("searchdomain", config.Searchdomain)
	d.Set("nameserver", config.Nameserver)
	d.Set("sshkeys", config.Sshkeys)
	d.Set("ipconfig0", config.Ipconfig[0])
	d.Set("ipconfig1", config.Ipconfig[1])
	d.Set("ipconfig2", config.Ipconfig[2])
	d.Set("ipconfig3", config.Ipconfig[3])
	d.Set("ipconfig4", config.Ipconfig[4])
	d.Set("ipconfig5", config.Ipconfig[5])
	d.Set("ipconfig6", config.Ipconfig[6])
	d.Set("ipconfig7", config.Ipconfig[7])
	d.Set("ipconfig8", config.Ipconfig[8])
	d.Set("ipconfig9", config.Ipconfig[9])
	d.Set("ipconfig10", config.Ipconfig[10])
	d.Set("ipconfig11", config.Ipconfig[11])
	d.Set("ipconfig12", config.Ipconfig[12])
	d.Set("ipconfig13", config.Ipconfig[13])
	d.Set("ipconfig14", config.Ipconfig[14])
	d.Set("ipconfig15", config.Ipconfig[15])

	d.Set("smbios", ReadSmbiosArgs(config.Smbios1))

	// Some dirty hacks to populate undefined keys with default values.
	// TODO: remove "oncreate" handling in next major release.
	checkedKeys := []string{"force_create", "define_connection_info", "oncreate"}
	for _, key := range checkedKeys {
		if val := d.Get(key); val == nil {
			logger.Debug().Int("vmid", vmID).Msgf("key '%s' not found, setting to default", key)
			d.Set(key, thisResource.Schema[key].Default)
		} else {
			logger.Debug().Int("vmid", vmID).Msgf("key '%s' is set to %t", key, val.(bool))
			d.Set(key, val.(bool))
		}
	}
	// Check "full_clone" separately, as it causes issues in loop above due to how GetOk returns values on false bools.
	// Since "full_clone" has a default of true, it will always be in the configuration, so no need to verify.
	d.Set("full_clone", d.Get("full_clone"))

	// Disks.
	// add an explicit check that the keys in the config.QemuDisks map are a strict subset of
	// the keys in our resource schema. if they aren't things fail in a very weird and hidden way
	for _, diskEntry := range config.QemuDisks {
		for key := range diskEntry {
			if _, ok := thisResource.Schema["disk"].Elem.(*schema.Resource).Schema[key]; !ok {
				if key == "id" { // we purposely ignore id here as that is implied by the order in the TypeList/QemuDevice(list)
					continue
				}
				if !pconf.DangerouslyIgnoreUnknownAttributes {
					return fmt.Errorf("proxmox Provider Error: proxmox API returned new disk parameter '%v' we cannot process", key)
				}
			}
		}
	}

	// need to set cache because proxmox-api-go requires a value for cache but doesn't return a value for
	// it when it is empty. thus if cache is "" then we should insert "none" instead for consistency
	var rxCloudInitDrive = regexp.MustCompile(`^.*-cloudinit.*`)
	for id, qemuDisk := range config.QemuDisks {
		logger.Debug().Int("vmid", vmID).Msgf("[READ] Disk Processed '%v'", qemuDisk)
		// ugly hack to avoid cloudinit disk to be removed since they usually are not present in resource definition
		// but are created from proxmox as ide2 so threated
		if ciDisk := rxCloudInitDrive.FindStringSubmatch(qemuDisk["file"].(string)); len(ciDisk) > 0 {
			config.QemuDisks[id] = nil
			logger.Debug().Int("vmid", vmID).Msgf("[READ] Remove cloudinit disk")
		}
		// cache == "none" is required for disk creation/updates but proxmox-api-go returns cache == "" or cache == nil in reads
		if qemuDisk["cache"] == "" || qemuDisk["cache"] == nil {
			qemuDisk["cache"] = "none"
		}
		// backup is default true but state must be set!
		if qemuDisk["backup"] == "" || qemuDisk["backup"] == nil {
			qemuDisk["backup"] = true
		} // else if qemuDisk["backup"] == true {
		// 	qemuDisk["backup"] = 1
		// }
	}

	flatDisks, _ := FlattenDevicesList(config.QemuDisks)
	flatDisks, _ = DropElementsFromMap([]string{"id"}, flatDisks)
	if d.Set("disk", flatDisks); err != nil {
		return err
	}

	// read in the qemu hostpci
	qemuPCIDevices, _ := FlattenDevicesList(config.QemuPCIDevices)
	logger.Debug().Int("vmid", vmID).Msgf("Hostpci Block Processed '%v'", config.QemuPCIDevices)
	if d.Set("hostpci", qemuPCIDevices); err != nil {
		return err
	}

	// read in the qemu hostpci
	qemuUsbsDevices, _ := FlattenDevicesList(config.QemuUsbs)
	logger.Debug().Int("vmid", vmID).Msgf("Usb Block Processed '%v'", config.QemuUsbs)
	if d.Set("usb", qemuUsbsDevices); err != nil {
		return err
	}

	// read in the unused disks
	flatUnusedDisks, _ := FlattenDevicesList(config.QemuUnusedDisks)
	logger.Debug().Int("vmid", vmID).Msgf("Unused Disk Block Processed '%v'", config.QemuUnusedDisks)
	if d.Set("unused_disk", flatUnusedDisks); err != nil {
		return err
	}

	// Display.
	activeVgaSet := d.Get("vga").(*schema.Set)
	if len(activeVgaSet.List()) > 0 {
		d.Set("features", UpdateDeviceConfDefaults(config.QemuVga, activeVgaSet))
	}

	// Networks.
	// add an explicit check that the keys in the config.QemuNetworks map are a strict subset of
	// the keys in our resource schema. if they aren't things fail in a very weird and hidden way
	logger.Debug().Int("vmid", vmID).Msgf("Analyzing Network blocks ")
	for _, networkEntry := range config.QemuNetworks {
		logger.Debug().Int("vmid", vmID).Msgf("Network block received '%v'", networkEntry)
		// If network tag was not set, assign default value.
		if networkEntry["tag"] == "" || networkEntry["tag"] == nil {
			networkEntry["tag"] = thisResource.Schema["network"].Elem.(*schema.Resource).Schema["tag"].Default
		}
		for key := range networkEntry {
			if _, ok := thisResource.Schema["network"].Elem.(*schema.Resource).Schema[key]; !ok {
				if key == "id" { // we purposely ignore id here as that is implied by the order in the TypeList/QemuDevice(list)
					continue
				}
				return fmt.Errorf("proxmox Provider Error: proxmox API returned new network parameter '%v' we cannot process", key)
			}
		}
	}
	// flatten the structure into the format terraform needs and remove the "id" attribute as that will be encoded into
	// the list structure.
	flatNetworks, _ := FlattenDevicesList(config.QemuNetworks)
	flatNetworks, _ = DropElementsFromMap([]string{"id"}, flatNetworks)
	if err = d.Set("network", flatNetworks); err != nil {
		return err
	}

	d.Set("pool", vmr.Pool())
	// Serials
	configSerialsSet := d.Get("serial").(*schema.Set)
	activeSerialSet := UpdateDevicesSet(configSerialsSet, config.QemuSerials, "id")
	d.Set("serial", activeSerialSet)

	// Reset reboot_required variable. It should change only during updates.
	d.Set("reboot_required", false)

	// Pool
	pools, err := client.GetPoolList()
	if err == nil {
		for _, poolInfo := range pools["data"].([]interface{}) {
			poolContent, _ := client.GetPoolInfo(poolInfo.(map[string]interface{})["poolid"].(string))
			for _, member := range poolContent["members"].([]interface{}) {
				if member.(map[string]interface{})["type"] != "storage" {
					if vmID == int(member.(map[string]interface{})["vmid"].(float64)) {
						d.Set("pool", poolInfo.(map[string]interface{})["poolid"].(string))
					}
				}
			}
		}
	}

	// DEBUG print out the read result
	flatValue, _ := resourceDataToFlatValues(d, thisResource)
	jsonString, _ := json.Marshal(flatValue)
	if len(flatNetworks) > 0 {
		logger.Debug().Int("vmid", vmID).Msgf("VM Net Config '%+v' from '%+v' set as '%+v' type of '%T'", config.QemuNetworks, flatNetworks, d.Get("network"), flatNetworks[0]["macaddr"])
	}
	logger.Debug().Int("vmid", vmID).Msgf("Finished VM read resulting in data: '%+v'", string(jsonString))

	return nil
}

func resourceVmQemuDelete(d *schema.ResourceData, meta interface{}) error {
	pconf := meta.(*providerConfiguration)
	lock := pmParallelBegin(pconf)
	defer lock.unlock()

	client := pconf.Client
	vmId, _ := strconv.Atoi(path.Base(d.Id()))
	vmr := pxapi.NewVmRef(vmId)
	vmState, err := client.GetVmState(vmr)
	if err != nil {
		return err
	}
	if vmState["status"] != "stopped" {
		_, err := client.StopVm(vmr)
		if err != nil {
			return err
		}

		// Wait until vm is stopped. Otherwise, deletion will fail.
		// ugly way to wait 5 minutes(300s)
		waited := 0
		for waited < 300 {
			vmState, err := client.GetVmState(vmr)
			if err == nil && vmState["status"] == "stopped" {
				break
			} else if err != nil {
				return err
			}
			// wait before next try
			time.Sleep(5 * time.Second)
			waited += 5
		}
	}

	_, err = client.DeleteVm(vmr)
	return err
}

// Increase disk size if original disk was smaller than new disk.
func prepareDiskSize(
	client *pxapi.Client,
	vmr *pxapi.VmRef,
	diskConfMap pxapi.QemuDevices,
	d *schema.ResourceData,
) error {
	logger, _ := CreateSubLogger("prepareDiskSize")
	clonedConfig, err := pxapi.NewConfigQemuFromApi(vmr, client)
	if err != nil {
		return err
	}
	// log.Printf("%s", clonedConfig)
	for diskID, diskConf := range diskConfMap {
		if diskConf["media"] == "cdrom" {
			continue
		}
		diskName := fmt.Sprintf("%v%v", diskConf["type"], diskID)

		diskSize := pxapi.DiskSizeGB(diskConf["size"])

		if _, diskExists := clonedConfig.QemuDisks[diskID]; !diskExists {
			return err
		}

		clonedDiskSize := pxapi.DiskSizeGB(clonedConfig.QemuDisks[diskID]["size"])

		if err != nil {
			return err
		}

		logger.Debug().Int("diskId", diskID).Msgf("Checking disk sizing. Original '%+v', New '%+v'", fmt.Sprintf("%vG", clonedDiskSize), fmt.Sprintf("%vG", diskSize))
		if diskSize > clonedDiskSize {
			logger.Debug().Int("diskId", diskID).Msgf("Resizing disk")
			for ii := 0; ii < 5; ii++ {
				_, err = client.ResizeQemuDiskRaw(vmr, diskName, fmt.Sprintf("%vG", diskSize))
				if err == nil {
					break
				}
				logger.Debug().Int("diskId", diskID).Msgf("Error returned from api: %+v", err)
				// wait before next try
				time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)
			}
		} else if diskSize == clonedDiskSize || diskSize <= 0 {
			logger.Debug().Int("diskId", diskID).Msgf("Disk is same size as before, skipping resize. Original '%+v', New '%+v'", fmt.Sprintf("%vG", clonedDiskSize), fmt.Sprintf("%vG", diskSize))
		} else {
			return fmt.Errorf("proxmox does not support decreasing disk size. Disk '%v' wanted to go from '%vG' to '%vG'", diskName, fmt.Sprintf("%vG", clonedDiskSize), fmt.Sprintf("%vG", diskSize))
		}

	}
	return nil
}

// Converting from schema.TypeSet to map of id and conf for each device,
// which will be sent to Proxmox API.
func DevicesSetToMap(devicesSet *schema.Set) (pxapi.QemuDevices, error) {

	var err error
	devicesMap := pxapi.QemuDevices{}

	for _, set := range devicesSet.List() {
		setMap, isMap := set.(map[string]interface{})
		if isMap {
			setID := setMap["id"].(int)
			if _, ok := devicesMap[setID]; !ok {
				devicesMap[setID] = setMap
			} else {
				return nil, fmt.Errorf("unable to process set, received a duplicate ID '%v' check your configuration file", setID)
			}
		}
	}
	return devicesMap, err
}

// Drops an element from each map in a []map[string]interface{}
// this allows a quick and easy way to remove things like "id" that is added by the proxmox api go library
// when we instead encode that id as the list index (and thus terraform would reject it in a d.Set(..) call
// WARNING mutates the list fed in!  make a copy if you need to keep the original
func DropElementsFromMap(elements []string, mapList []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, mapItem := range mapList {
		for _, elem := range elements {
			delete(mapItem, elem)
		}
	}
	return mapList, nil
}

// Consumes an API return (pxapi.QemuDevices) and "flattens" it into a []map[string]interface{} as
// expected by the terraform interface for TypeList
func FlattenDevicesList(proxmoxDevices pxapi.QemuDevices) ([]map[string]interface{}, error) {
	flattenedDevices := make([]map[string]interface{}, 0, 1)

	numDevices := len(proxmoxDevices)
	if numDevices == 0 {
		return flattenedDevices, nil
	}

	// QemuDevices is a map[int]map[string]interface{}
	// we loop by index here to ensure that the devices remain in the same order
	for index := 0; index < numDevices; index++ {
		thisDevice := proxmoxDevices[index]
		thisFlattenedDevice := make(map[string]interface{})

		if thisDevice == nil {
			continue
		}

		for configuration, value := range thisDevice {
			thisFlattenedDevice[configuration] = value
		}

		flattenedDevices = append(flattenedDevices, thisFlattenedDevice)
	}

	return flattenedDevices, nil
}

func BuildSmbiosArgs(smbiosList []interface{}) string {
	if len(smbiosList) == 0 {
		return ""
	}

	smbiosArgs := []string{}
	for _, v := range smbiosList {
		for conf, value := range v.(map[string]interface{}) {
			switch conf {

			case "uuid":
				var s string
				if value.(string) == "" {
					s = fmt.Sprintf("%s=%s", conf, uuid.New().String())
				} else {
					s = fmt.Sprintf("%s=%s", conf, value.(string))
				}
				smbiosArgs = append(smbiosArgs, s)

			case "serial", "manufacturer", "product", "version", "sku", "family":
				if value.(string) == "" {
					continue
				}
				s := fmt.Sprintf("%s=%s", conf, base64.StdEncoding.EncodeToString([]byte(value.(string))))
				smbiosArgs = append(smbiosArgs, s)

			default:
				continue
			}
		}
	}
	smbiosArgs = append(smbiosArgs, "base64=1")

	return strings.Join(smbiosArgs, ",")
}

func ReadSmbiosArgs(smbios string) []interface{} {
	if smbios == "" {
		return nil
	}

	smbiosArgs := []interface{}{}
	smbiosMap := make(map[string]interface{}, 0)
	for _, l := range strings.Split(smbios, ",") {
		if l == "" {
			continue
		}
		k, v := strings.Split(l, "=")[0], strings.Split(l, "=")[1]
		decodedString, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			decodedString = []byte(v)
		}
		smbiosMap[k] = string(decodedString)
	}

	return append(smbiosArgs, smbiosMap)
}

// Consumes a terraform TypeList of a Qemu Device (network, hard drive, etc) and returns the "Expanded"
// version of the equivalent configuration that the API understands (the struct pxapi.QemuDevices).
// NOTE this expects the provided deviceList to be []map[string]interface{}.
func ExpandDevicesList(deviceList []interface{}) (pxapi.QemuDevices, error) {
	expandedDevices := make(pxapi.QemuDevices)

	if len(deviceList) == 0 {
		return expandedDevices, nil
	}

	for index, deviceInterface := range deviceList {
		thisDeviceMap := deviceInterface.(map[string]interface{})

		// allocate an expandedDevice, we'll append it to the list at the end of this loop
		thisExpandedDevice := make(map[string]interface{})

		// bail out if the device is empty, it is meaningless in this context
		if thisDeviceMap == nil {
			continue
		}

		// this is a map of string->interface, loop over it and move it into
		// the qemuDevices struct
		for configuration, value := range thisDeviceMap {
			thisExpandedDevice[configuration] = value
		}

		expandedDevices[index] = thisExpandedDevice
	}

	return expandedDevices, nil
}

// Update schema.TypeSet with new values comes from Proxmox API.
// TODO: remove these set functions and convert attributes using a set to a list instead.
func UpdateDevicesSet(
	devicesSet *schema.Set,
	devicesMap pxapi.QemuDevices,
	idKey string,
) *schema.Set {

	// configDevicesMap, _ := DevicesSetToMap(devicesSet)

	// activeDevicesMap := updateDevicesDefaults(devicesMap, configDevicesMap)
	activeDevicesMap := devicesMap

	for _, setConf := range devicesSet.List() {
		devicesSet.Remove(setConf)
		setConfMap := setConf.(map[string]interface{})
		deviceID := setConfMap[idKey].(int)
		setConfMap = adaptDeviceToConf(setConfMap, activeDevicesMap[deviceID])
		devicesSet.Add(setConfMap)
	}

	return devicesSet
}

func initConnInfo(
	d *schema.ResourceData,
	pconf *providerConfiguration,
	client *pxapi.Client,
	vmr *pxapi.VmRef,
	config *pxapi.ConfigQemu,
	lock *pmApiLockHolder,
) error {

	logger, _ := CreateSubLogger("initConnInfo")

	var err error
	var lasterr error
	var interfaces []pxapi.AgentNetworkInterface
	// allow user to opt-out of setting the connection info for the resource
	if !d.Get("define_connection_info").(bool) {
		log.Printf("[INFO][initConnInfo] define_connection_info is %t, no further action", d.Get("define_connection_info").(bool))
		logger.Info().Int("vmid", vmr.VmId()).Msgf("define_connection_info is %t, no further action", d.Get("define_connection_info").(bool))

		return nil
	}
	// allow user to opt-out of setting the connection info for the resource
	if d.Get("agent") != 1 {
		log.Printf("[INFO][initConnInfo] qemu agent is disabled from proxmox config, cant comunicate with vm.")
		logger.Info().Int("vmid", vmr.VmId()).Msgf("qemu agent is disabled from proxmox config, cant comunicate with vm.")
		return nil
	}

	log.Print("[INFO][initConnInfo] trying to get vm ip address for provisioner")
	logger.Info().Int("vmid", vmr.VmId()).Msgf("trying to get vm ip address for provisioner")
	sshPort := "22"
	sshHost := ""
	// assume guest agent not running yet or not enabled
	guestAgentRunning := false

	// wait until the os has started the guest agent
	guestAgentTimeout := d.Timeout(schema.TimeoutCreate)
	guestAgentWaitEnd := time.Now().Add(time.Duration(guestAgentTimeout))
	log.Printf("[DEBUG][initConnInfo] retrying for at most  %v minutes before giving up", guestAgentTimeout)
	log.Printf("[DEBUG][initConnInfo] retries will end at %s", guestAgentWaitEnd)
	logger.Debug().Int("vmid", vmr.VmId()).Msgf("retrying for at most  %v minutes before giving up", guestAgentTimeout)
	logger.Debug().Int("vmid", vmr.VmId()).Msgf("retries will end at %s", guestAgentWaitEnd)

	for time.Now().Before(guestAgentWaitEnd) {
		interfaces, err = client.GetVmAgentNetworkInterfaces(vmr)
		lasterr = err
		if err != nil {
			log.Printf("[DEBUG][initConnInfo] check ip result error %s", err.Error())
			logger.Debug().Int("vmid", vmr.VmId()).Msgf("check ip result error %s", err.Error())
		} else if err == nil {
			lasterr = nil
			log.Print("[INFO][initConnInfo] found working QEMU Agent")
			log.Printf("[DEBUG][initConnInfo] interfaces found: %v", interfaces)
			logger.Info().Int("vmid", vmr.VmId()).Msgf("found working QEMU Agent")
			logger.Debug().Int("vmid", vmr.VmId()).Msgf("interfaces found: %v", interfaces)

			guestAgentRunning = true
			break
		} else if !strings.Contains(err.Error(), "500 QEMU guest agent is not running") {
			// "not running" means either not installed or not started yet.
			// any other error should not happen here
			return err
		}
		// wait before next try
		time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)
	}
	if lasterr != nil {
		log.Printf("[INFO][initConnInfo] error from PVE: \"%s\"\n, QEMU Agent is enabled in you configuration but non installed/not working on your vm", lasterr)
		logger.Info().Int("vmid", vmr.VmId()).Msgf("error from PVE: \"%s\"\n, QEMU Agent is enabled in you configuration but non installed/not working on your vm", lasterr)
		return fmt.Errorf("error from PVE: \"%s\"\n, QEMU Agent is enabled in you configuration but non installed/not working on your vm", lasterr)
	}
	vmConfig, err := client.GetVmConfig(vmr)
	if err != nil {
		return err
	}
	log.Print("[INFO][initConnInfo] trying to find IP address of first network card")
	logger.Info().Int("vmid", vmr.VmId()).Msgf("trying to find IP address of first network card")

	// wait until we find a valid ipv4 address
	log.Printf("[DEBUG][initConnInfo] checking network card...")
	logger.Debug().Int("vmid", vmr.VmId()).Msgf("checking network card...")
	for guestAgentRunning && time.Now().Before(guestAgentWaitEnd) {
		log.Printf("[DEBUG][initConnInfo] checking network card...")
		interfaces, err = client.GetVmAgentNetworkInterfaces(vmr)
		net0MacAddress := macAddressRegex.FindString(vmConfig["net0"].(string))
		if err != nil {
			log.Printf("[DEBUG][initConnInfo] checking network card error %s", err.Error())
			logger.Debug().Int("vmid", vmr.VmId()).Msgf("checking network card error %s", err.Error())
			// return err
		} else {
			log.Printf("[DEBUG][initConnInfo] checking network card loop")
			logger.Debug().Int("vmid", vmr.VmId()).Msgf("checking network card loop")
			for _, iface := range interfaces {
				if strings.EqualFold(strings.ToUpper(iface.MACAddress), strings.ToUpper(net0MacAddress)) {
					for _, addr := range iface.IPAddresses {
						if addr.IsGlobalUnicast() && strings.Count(addr.String(), ":") < 2 {
							log.Printf("[DEBUG][initConnInfo] Found IP address: %s", addr.String())
							logger.Debug().Int("vmid", vmr.VmId()).Msgf("Found IP address: %s", addr.String())
							sshHost = addr.String()
						}
					}
				}
			}
			if sshHost != "" {
				log.Printf("[DEBUG][initConnInfo] sshHost not empty: %s", sshHost)
				logger.Debug().Int("vmid", vmr.VmId()).Msgf("sshHost not empty: %s", sshHost)
				break
			}
		}
		// wait before next try
		time.Sleep(time.Duration(d.Get("additional_wait").(int)) * time.Second)
	}
	// todo - log a warning if we couldn't get an IP

	if config.HasCloudInit() {
		log.Print("[DEBUG][initConnInfo] vm has a cloud-init configuration")
		logger.Debug().Int("vmid", vmr.VmId()).Msgf(" vm has a cloud-init configuration")
		_, ipconfig0Set := d.GetOk("ipconfig0")
		if ipconfig0Set {
			vmState, err := client.GetVmState(vmr)
			log.Printf("[DEBUG][initConnInfo] cloudinitcheck vm state %v", vmState)
			logger.Debug().Int("vmid", vmr.VmId()).Msgf("cloudinitcheck vm state %v", vmState)
			if err != nil {
				log.Printf("[DEBUG][initConnInfo] vmstate error %s", err.Error())
				logger.Debug().Int("vmid", vmr.VmId()).Msgf("vmstate error %s", err.Error())
				return err
			}

			if d.Get("ipconfig0").(string) != "ip=dhcp" || vmState["agent"] == nil || vmState["agent"].(float64) != 1 {
				// parse IP address out of ipconfig0
				ipMatch := rxIPconfig.FindStringSubmatch(d.Get("ipconfig0").(string))
				if sshHost == "" {
					sshHost = ipMatch[1]
				}
				ipconfig0 := net.ParseIP(strings.Split(ipMatch[1], ":")[0])
				interfaces, err = client.GetVmAgentNetworkInterfaces(vmr)
				log.Printf("[DEBUG][initConnInfo] ipconfig0 interfaces: %v", interfaces)
				logger.Debug().Int("vmid", vmr.VmId()).Msgf("ipconfig0 interfaces %v", interfaces)
				if err != nil {
					return err
				} else {
					for _, iface := range interfaces {
						if sshHost == ipMatch[1] {
							break
						}
						for _, addr := range iface.IPAddresses {
							if addr.Equal(ipconfig0) {
								sshHost = ipMatch[1]
								break
							}
						}
					}
				}
			}
		}

		log.Print("[DEBUG][initConnInfo] found an ip configuration")
		logger.Debug().Int("vmid", vmr.VmId()).Msgf("Found an ip configuration")
		// Check if we got a speficied port
		if strings.Contains(sshHost, ":") {
			sshParts := strings.Split(sshHost, ":")
			sshHost = sshParts[0]
			sshPort = sshParts[1]
		}
	}
	// This code is legacy
	// else {
	// 	log.Print("[DEBUG] setting up SSH forward")
	// 	if d.Get("ssh_forward_ip") != nil {
	// 		sshHost = d.Get("ssh_forward_ip").(string)
	// 		sshPort, err = pxapi.SshForwardUsernet(vmr, client)
	// 		if err != nil {
	// 			return err
	// 		}
	// 	}

	// }

	// Done with proxmox API, end parallel and do the SSH things
	// lock.unlock()
	if sshHost == "" {
		log.Print("[DEBUG][initConnInfo] Cannot find any IP address")
		logger.Debug().Int("vmid", vmr.VmId()).Msgf("Cannot find any IP address")
		return fmt.Errorf("cannot find any IP address")
	}

	log.Printf("[DEBUG][initConnInfo] this is the vm configuration: %s %s", sshHost, sshPort)
	logger.Debug().Int("vmid", vmr.VmId()).Msgf("this is the vm configuration: %s %s", sshHost, sshPort)

	// Optional convience attributes for provisioners
	d.Set("default_ipv4_address", sshHost)
	d.Set("ssh_host", sshHost)
	d.Set("ssh_port", sshPort)

	// This connection INFO is longer shared up to the providers :-(
	d.SetConnInfo(map[string]string{
		"type": "ssh",
		"host": sshHost,
		"port": sshPort,
		// "user":            d.Get("ssh_user").(string),
		// "private_key":     d.Get("ssh_private_key").(string),
		// not sure what the following stuff was for?!
		// "pm_api_url":      client.ApiUrl,
		// "pm_user":         client.Username,
		// "pm_password":     client.Password,
		// "pm_otp":          client.Otp,
		// "pm_tls_insecure": "true", // TODO - pass pm_tls_insecure state around, but if we made it this far, default insecure
	})
	return nil
}