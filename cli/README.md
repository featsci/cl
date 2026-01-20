## TODO: Automation & Deployment

- [ ] **Implement VM Creation** (`Create new VMs in CLO.ru`)
    - [ ] Implement flag `-add`
    - [ ] Support masters count: `-m <count>` (e.g., `-m 3`)
    - [ ] Support workers count: `-w <count>` (e.g., `-w 5`)
- [ ] **Prepare Kubespray Configuration**
    - [ ] Reference: `gemini/go-containerregistry/1/main.go`
    - [ ] Disable all Ingress controllers
    - [ ] Disable Managed Load Balancers (MLB)
    - [ ] Create Pull Request with these changes
- [ ] **Deploy Kubernetes**
    - [ ] Run Kubespray deployment with the updated config
- [ ] **Implement VM Deletion** (`Delete new VMs in CLO.ru`)
    - [ ] Implement flag `-del`
    - [ ] Add flag to preserve disks: `-skip disks`