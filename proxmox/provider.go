package dutchis

import (
	"crypto/tls"
	"fmt"
    "net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type providerConfiguration struct {
	Client                             *http.Client
	MaxParallel                        int
	CurrentParallel                    int
	MaxVMID                            int
	Mutex                              *sync.Mutex
	Cond                               *sync.Cond
	LogFile                            string
	LogLevels                          map[string]string
	DangerouslyIgnoreUnknownAttributes bool
}

// Provider - Terrafrom properties for dutchis
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"dutchis_team_uuid": {
				Type:        schema.TypeString,
				Optional:    false,
				DefaultFunc: schema.EnvDefaultFunc("DUTCHIS_TEAM_UUID", nil),
				Description: "Team UUID to which to deploy to.",
			},
			"dutchis_api_token": {
				Type:        schema.TypeString,
				Optional:    false,
				DefaultFunc: schema.EnvDefaultFunc("DUTCHIS_API_TOKEN", nil),
				Description: "API Secret",
				Sensitive:   true,
			},
			"dutchis_log_enable": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Enable provider logging to get DutchIS API logs",
			},
			"dutchis_log_levels": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Configure the logging level to display; trace, debug, info, warn, etc",
			},
			"dutchis_log_file": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "terraform-plugin-dutchis.log",
				Description: "Write logs to this specific file",
			},
		},

		ResourcesMap: map[string]*schema.Resource{
			"proxmox_vm_qemu":  resourceVmQemu(),
			"proxmox_pool":     resourcePool(),
		},

		ConfigureFunc: providerConfigure,
	}
}

func providerConfigure(d *schema.ResourceData) (interface{}, error) {
	client, err := getClient(
		d.Get("dutchis_team_uuid").(string),
		d.Get("dutchis_api_token").(string),
		d.Get("dutchis_log_enable").(bool),
		d.Get("dutchis_log_levels").(string),
		d.Get("dutchis_log_file").(string),
	)
	if err != nil {
		return nil, err
	}

	// Minimum permissions check
	minimum_permissions := []string{
		"virtualserver:read",
		"virtualserver:create",
		"virtualserver:update",
		"virtualserver:power",
		"virtualserver:delete",
		"virtualserver:upgrade",
	}

	var id string
	if result, getok := d.GetOk("pm_api_token_id"); getok {
		id = result.(string)
		id = strings.Split(id, "!")[0]
	} else if result, getok := d.GetOk("pm_user"); getok {
		id = result.(string)
	}
	userID, err := pxapi.NewUserID(id)
	if err != nil {
		return nil, err
	}
	permlist, err := client.GetUserPermissions(userID, "/")
	if err != nil {
		return nil, err
	}
	sort.Strings(permlist)
	sort.Strings(minimum_permissions)
	permDiff := permissions_check(permlist, minimum_permissions)
	if len(permDiff) == 0 {
		// look to see what logging we should be outputting according to the provider configuration
		logLevels := make(map[string]string)
		for logger, level := range d.Get("pm_log_levels").(map[string]interface{}) {
			levelAsString, ok := level.(string)
			if ok {
				logLevels[logger] = levelAsString
			} else {
				return nil, fmt.Errorf("invalid logging level %v for %v. Be sure to use a string", level, logger)
			}
		}

		// actually configure logging
		// note that if enable is false here, the configuration will squash all output
		ConfigureLogger(
			d.Get("pm_log_enable").(bool),
			d.Get("pm_log_file").(string),
			logLevels,
		)

		var mut sync.Mutex
		return &providerConfiguration{
			Client:                             client,
			MaxParallel:                        d.Get("pm_parallel").(int),
			CurrentParallel:                    0,
			MaxVMID:                            -1,
			Mutex:                              &mut,
			Cond:                               sync.NewCond(&mut),
			LogFile:                            d.Get("pm_log_file").(string),
			LogLevels:                          logLevels,
			DangerouslyIgnoreUnknownAttributes: d.Get("pm_dangerously_ignore_unknown_attributes").(bool),
		}, nil
	} else {
		err = fmt.Errorf("permissions for user/token %s are not sufficient, please provide also the following permissions that are missing: %v", userID.ToString(), permDiff)
		return nil, err
	}
}

func getClient(
	dutchis_team_uuid string,
	dutchis_api_token string,
	dutchis_log_enable bool,
	dutchis_log_levels string,
	dutchis_log_file string,
) (*http.Client, error) {
	var err error

	if dutchis_team_uuid == "" {
		err = fmt.Errorf("You did not provide a team uuid")
	}
	
	if dutchis_api_token == "" {
		err = fmt.Errorf("You did not provide an API token")
	}

    client := &http.Client{}

	if err != nil {
		return nil, err
	}

	return client, nil
}

type pmApiLockHolder struct {
	locked bool
	pconf  *providerConfiguration
}

func (lock *pmApiLockHolder) lock() {
	if lock.locked {
		return
	}
	lock.locked = true
	pconf := lock.pconf
	pconf.Mutex.Lock()
	for pconf.CurrentParallel >= pconf.MaxParallel {
		pconf.Cond.Wait()
	}
	pconf.CurrentParallel++
	pconf.Mutex.Unlock()
}

func (lock *pmApiLockHolder) unlock() {
	if !lock.locked {
		return
	}
	lock.locked = false
	pconf := lock.pconf
	pconf.Mutex.Lock()
	pconf.CurrentParallel--
	pconf.Cond.Signal()
	pconf.Mutex.Unlock()
}

func pmParallelBegin(pconf *providerConfiguration) *pmApiLockHolder {
	lock := &pmApiLockHolder{
		pconf:  pconf,
		locked: false,
	}
	lock.lock()
	return lock
}

func resourceId(targetNode string, resType string, vmId int) string {
	return fmt.Sprintf("%s/%s/%d", targetNode, resType, vmId)
}

func parseResourceId(resId string) (targetNode string, resType string, vmId int, err error) {
	if !rxRsId.MatchString(resId) {
		return "", "", -1, fmt.Errorf("invalid resource format: %s. Must be <node>/<type>/<vmid>", resId)
	}
	idMatch := rxRsId.FindStringSubmatch(resId)
	targetNode = idMatch[1]
	resType = idMatch[2]
	vmId, err = strconv.Atoi(idMatch[3])
	return
}

func clusterResourceId(resType string, resId string) string {
	return fmt.Sprintf("%s/%s", resType, resId)
}

func parseClusterResourceId(resId string) (resType string, id string, err error) {
	if !rxClusterRsId.MatchString(resId) {
		return "", "", fmt.Errorf("invalid resource format: %s. Must be <type>/<resourceid>", resId)
	}
	idMatch := rxClusterRsId.FindStringSubmatch(resId)
	return idMatch[1], idMatch[2], nil
}
