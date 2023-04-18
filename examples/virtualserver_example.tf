terraform {
    required_providers {
        dutchis = {
            source  = "dutchis/terraform"
            version = ">= 2.9.5"
        }
    }
}

provider "dutchis" {
    dutchis_team_uuid = "<your-team-uuid>"
    dutchis_api_token = "<your-api-token>"
}

resource "virtualserver" "example-vs" {
    hostname = "example-vs" # Hostname of the virtual server
    class = "performance" # Performance class
    os = "ubuntu2204" # OS id
    username = "exampleuser" # Ignored on windows systems
    password = "ferrysekur" # Default user's password
    sshkeys = [
        "ssh-rsa AAAAB3NzaC1yc2EAAA...", # OpenSSH format
        "7f801119-cf84-4f61-87bc-dec7059978e5" # Or a sshkey uuid
    ]
    cores = 2 # Number of cores
    memory = 4 # Memory in GB
    network = 1 # Network speed in Gbps
    disk = 50 # Disk speed in GB
}
