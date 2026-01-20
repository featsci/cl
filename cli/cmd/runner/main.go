package main

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	KubesprayImage = "quay.io/kubespray/kubespray:v2.29.1"
	FluxImage      = "ghcr.io/fluxcd/flux-cli:v2.7.5"
	KubesprayFS    = "/root/kubespray-fs"
	FluxFS         = "/root/flux-fs"
)

const LoadImagesYml = `
---
- hosts: k8s_cluster
  become: true
  gather_facts: false
  vars:
    bundle_dest: "/root/k8s_images.tar" 
  tasks:
    - name: "[Debug] Check source bundle on Bastion"
      stat:
        path: /root/k8s_images.tar
      delegate_to: localhost
      register: bundle_stat

    - name: Fail if bundle is missing
      fail:
        msg: "CRITICAL: /root/k8s_images.tar not found on Bastion!"
      when: not bundle_stat.stat.exists

    - name: Clean old bundle on Node
      file:
        path: "{{ bundle_dest }}"
        state: absent

    - name: Copy images bundle to Node (Force)
      copy:
        src: /root/k8s_images.tar
        dest: "{{ bundle_dest }}"
        mode: '0644'
        force: yes

    - name: Import images to Containerd (Ignore Errors)
      shell: "ctr -n k8s.io images import --all-platforms {{ bundle_dest }}"
      register: import_out
      ignore_errors: yes
      environment:
        PATH: "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

    - name: Remove temp file
      file:
        path: "{{ bundle_dest }}"
        state: absent
`

const DiskSetupMainYml = `
---
- hosts: all
  become: true
  gather_facts: no
  
  tasks:
    - name: Configure disks from inventory
      include_tasks: setup-disk.yml
      loop: "{{ data_disks | default([], true) }}"
      loop_control:
        loop_var: current_disk
`

const DiskSetupTaskYml = `
- name: "1. Check for existence of {{ current_disk.device }}"
  stat:
    path: "{{ current_disk.device }}"
  register: disk_stat

- name: "2. Create folder {{ current_disk.mount }}"
  file:
    path: "{{ current_disk.mount }}"
    state: directory
    mode: '0755'
  when: disk_stat.stat.exists

- name: "3. Format {{ current_disk.device }} (SAFE)"
  filesystem:
    fstype: ext4
    dev: "{{ current_disk.device }}"
    force: no
    opts: "-F"
  when: disk_stat.stat.exists

- name: "3.5 UUID of {{ current_disk.device }}"
  command: "blkid -s UUID -o value {{ current_disk.device }}"
  register: disk_uuid_out
  changed_when: false
  when: disk_stat.stat.exists

- name: "4. Mount {{ current_disk.device }} -> {{ current_disk.mount }}"
  mount:
    path: "{{ current_disk.mount }}"
    src: "UUID={{ disk_uuid_out.stdout }}"
    fstype: ext4
    opts: defaults
    dump: '0'
    passno: '2'
    state: mounted
  when: disk_stat.stat.exists and disk_uuid_out.stdout != ""
`

const CreateUserYml = `
---
- hosts: all
  become: true
  gather_facts: false
  tasks:
    - name: Update Apt Cache
      apt:
        update_cache: yes
        cache_valid_time: 3600

    - name: Create user group
      group:
        name: "{{ target_user }}"
        state: present

    - name: Create user with sudo privileges
      user:
        name: "{{ target_user }}"
        shell: /bin/bash
        groups: sudo,root,adm
        append: yes
        create_home: yes

    - name: Set authorized key (SSH)
      authorized_key:
        user: "{{ target_user }}"
        key: "{{ lookup('file', '/root/new_user.pub') }}"
        state: present

    - name: Setup Passwordless Sudo
      lineinfile:
        path: "/etc/sudoers.d/{{ target_user }}"
        line: "{{ target_user }} ALL=(ALL) NOPASSWD: ALL"
        create: yes
        mode: '0440'
        validate: 'visudo -cf %s'
`

const OSUpdateYml = `
---
- hosts: all
  become: true
  gather_facts: false
  tasks:
    - name: Update Apt Cache
      apt:
        update_cache: yes
    
    - name: Upgrade System (Dist-Upgrade)
      apt:
        upgrade: dist
        force_apt_get: yes
      environment:
        DEBIAN_FRONTEND: noninteractive
    
    - name: Autoremove dependencies
      apt:
        autoremove: yes

    - name: Autoclean
      apt:
        autoclean: yes
`

// --- NEW PLAYBOOK ---
const PermissionsYml = `
---
- hosts: all
  become: true
  gather_facts: false
  tasks:
    - name: Set disk ownership and permissions recursively
      file:
        path: "{{ item.mount }}"
        owner: "{{ item.owner | default('root') }}"
        group: "{{ item.group | default('root') }}"
        mode: "{{ item.mode | default('0755') }}"
        state: directory
        recurse: yes
      loop: "{{ data_disks | default([], true) }}"
      when: 
        - item.mount is defined
        - item.owner is defined or item.group is defined or item.mode is defined
`

func main() {
	if len(os.Args) < 2 {
		parent("kubespray", []string{})
		return
	}
	command := os.Args[1]
	var restArgs []string
	if len(os.Args) > 2 {
		restArgs = os.Args[2:]
	}

	switch command {
	case "flux":
		parent("flux", restArgs)
	case "mount":
		parent("kubespray", append([]string{"mount"}, restArgs...))
	case "remove-node":
		parent("kubespray", append([]string{"remove-node"}, restArgs...))
	case "run":
		parent("kubespray", restArgs)
	case "create-user":
		parent("kubespray", append([]string{"create-user"}, restArgs...))
	case "os-update":
		parent("kubespray", append([]string{"os-update"}, restArgs...))
	case "permissions":
		parent("kubespray", append([]string{"permissions"}, restArgs...))
	case "child":
		mode := "kubespray"
		if len(os.Args) > 2 {
			mode = os.Args[2]
		}
		var childArgs []string
		if len(os.Args) > 3 {
			childArgs = os.Args[3:]
		}
		child(mode, childArgs)
	default:
		parent("kubespray", os.Args[1:])
	}
}

func parent(mode string, args []string) {
	var image, rootFS, marker string
	if mode == "flux" {
		image = FluxImage
		rootFS = FluxFS
		marker = filepath.Join(rootFS, "usr", "local", "bin", "flux")
	} else {
		image = KubesprayImage
		rootFS = KubesprayFS
		marker = filepath.Join(rootFS, "usr", "bin", "python3")
	}

	fmt.Printf("[HOST] Mode: %s. Checking FS (%s)...\n", mode, rootFS)
	needDownload := false
	if _, err := os.Stat(rootFS); os.IsNotExist(err) {
		needDownload = true
	} else {
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if mode == "kubespray" {
				if _, err2 := os.Stat(filepath.Join(rootFS, "usr", "local", "bin", "python3")); os.IsNotExist(err2) {
					needDownload = true
				}
			} else {
				needDownload = true
			}
		}
	}

	if needDownload {
		s3Url := os.Getenv("KUBESPRAY_URL")
		successS3 := false
		if s3Url != "" {
			fmt.Println("[GO] [S3] Downloading Runner image from S3...")
			if err := downloadFile(s3Url, "image.tar"); err != nil {
				fmt.Printf("[WARNING] S3 Error: %v. Trying Registry...\n", err)
			} else {
				os.MkdirAll(rootFS, 0755)
				exec.Command("tar", "-xf", "image.tar", "-C", rootFS).Run()
				os.Remove("image.tar")
				successS3 = true
				needDownload = false
			}
		}

		if !successS3 && needDownload {
			fmt.Printf("[HOST] FS missing. Downloading %s (Registry)...\n", image)
			os.MkdirAll(rootFS, 0755)
			if err := pullAndUnpack(image, rootFS); err != nil {
				fmt.Printf("[ERROR] Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("[+OK+] Image unpacked.")
		}
	} else {
		fmt.Println("[-=OKAY=-] FS exists.")
	}

	if mode == "kubespray" {
		k8sUrl := os.Getenv("K8S_IMAGES_URL")
		if k8sUrl != "" {
			targetDir := filepath.Join(rootFS, "root")
			os.MkdirAll(targetDir, 0755)
			targetFile := filepath.Join(targetDir, "k8s_images.tar")

			if _, err := os.Stat(targetFile); err == nil {
				fmt.Println("[RECYCLE] Deleting old local bundle (Force Update)...")
				os.Remove(targetFile)
			}

			fmt.Println("[GO] [S3] Downloading K8s Images Bundle...")
			if err := downloadFile(k8sUrl, targetFile); err != nil {
				fmt.Printf("[WARNING] Bundle download error: %v\n", err)
			} else {
				fmt.Printf("[+OK+] Bundle downloaded: %s\n", targetFile)
			}
		}
	}

	childCmdArgs := append([]string{"child", mode}, args...)
	cmd := exec.Command("/proc/self/exe", childCmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS}
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		fmt.Printf("Container error: %v\n", err)
		os.Exit(1)
	}
}

func child(mode string, args []string) {
	var rootFS string
	if mode == "flux" {
		rootFS = FluxFS
	} else {
		rootFS = KubesprayFS
	}

	syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, "")
	syscall.Sethostname([]byte(mode + "-runner"))
	copyFile("/etc/resolv.conf", filepath.Join(rootFS, "etc", "resolv.conf"))

	mounts := []struct {
		src, dst, ftype string
		flags           uintptr
	}{
		{"/dev", filepath.Join(rootFS, "dev"), "none", syscall.MS_BIND | syscall.MS_REC},
		{"/sys", filepath.Join(rootFS, "sys"), "none", syscall.MS_BIND | syscall.MS_REC},
		{"proc", filepath.Join(rootFS, "proc"), "proc", 0},
	}
	for _, m := range mounts {
		os.MkdirAll(m.dst, 0755)
		syscall.Mount(m.src, m.dst, m.ftype, m.flags, "")
	}

	hostCerts := "/etc/ssl/certs"
	targetCerts := filepath.Join(rootFS, "etc", "ssl", "certs")
	if _, err := os.Stat(hostCerts); err == nil {
		os.MkdirAll(targetCerts, 0755)
		syscall.Mount(hostCerts, targetCerts, "none", syscall.MS_BIND|syscall.MS_REC, "")
	}

	defer syscall.Unmount(filepath.Join(rootFS, "proc"), 0)
	defer syscall.Unmount(filepath.Join(rootFS, "sys"), 0)
	defer syscall.Unmount(filepath.Join(rootFS, "dev"), 0)

	if err := os.Chdir(rootFS); err != nil {
		panic(err)
	}
	if err := syscall.Chroot(rootFS); err != nil {
		panic(err)
	}
	os.Chdir("/")

	if mode == "flux" {
		runFlux()
	} else {
		if len(args) > 0 {
			switch args[0] {
			case "mount":
				runDiskSetup(args[1:])
			case "remove-node":
				runRemoveNode(args[1:])
			case "create-user":
				runCreateUser(args[1:])
			case "os-update":
				runOSUpdate(args[1:])
			case "permissions":
				runPermissions(args[1:])
			default:
				runKubespraySmart(args)
			}
		} else {
			runKubespraySmart(args)
		}
	}
}

func runPermissions(args []string) {
	fs := flag.NewFlagSet("permissions", flag.ContinueOnError)
	forksPtr := fs.Int("f", 5, "Ansible forks")
	limitPtr := fs.String("l", "", "Limit hosts")
	fs.Parse(args)

	ansibleBin, env, keyPath := setupAnsibleEnv()
	if ansibleBin == "" {
		return
	}

	fmt.Printf("[DISK/SEC] Applying disk permissions (Recursive). Forks: %d\n", *forksPtr)
	os.WriteFile("/permissions.yml", []byte(PermissionsYml), 0644)

	cmdArgs := []string{
		"-i", "/inventory.yaml",
		"--private-key", keyPath,
		"/permissions.yml",
		"-f", fmt.Sprintf("%d", *forksPtr),
	}
	if *limitPtr != "" {
		cmdArgs = append(cmdArgs, "-l", *limitPtr)
	}

	if err := runAnsible(ansibleBin, env, cmdArgs); err != nil {
		fmt.Printf("[ERROR] Permissions apply error: %v\n", err)
	} else {
		fmt.Println("[+OK+] Disk permissions applied successfully!")
	}
}

func runCreateUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	userPtr := fs.String("u", "", "Username")
	forksPtr := fs.Int("f", 5, "Ansible forks")
	limitPtr := fs.String("l", "", "Limit hosts")
	fs.Parse(args)

	if *userPtr == "" {
		fmt.Println("[ERROR] Error: Username not specified (-u)")
		return
	}

	ansibleBin, env, keyPath := setupAnsibleEnv()
	if ansibleBin == "" {
		return
	}

	fmt.Printf("[PACKAGE] Creating user '%s' (Forks: %d)...\n", *userPtr, *forksPtr)
	os.WriteFile("/create_user.yml", []byte(CreateUserYml), 0644)

	if _, err := os.Stat("/root/new_user.pub"); os.IsNotExist(err) {
		fmt.Println("[ERROR] Error: Public key not found (/root/new_user.pub).")
		return
	}

	cmdArgs := []string{
		"-i", "/inventory.yaml",
		"--private-key", keyPath,
		"/create_user.yml",
		"-e", fmt.Sprintf("target_user=%s", *userPtr),
		"-f", fmt.Sprintf("%d", *forksPtr),
	}
	if *limitPtr != "" {
		cmdArgs = append(cmdArgs, "-l", *limitPtr)
	}

	if err := runAnsible(ansibleBin, env, cmdArgs); err != nil {
		fmt.Printf("[ERROR] Execution error: %v\n", err)
	} else {
		fmt.Printf("[+OK+] User '%s' created!\n", *userPtr)
	}
}

func runOSUpdate(args []string) {
	fs := flag.NewFlagSet("os-update", flag.ContinueOnError)
	forksPtr := fs.Int("f", 5, "Ansible forks")
	limitPtr := fs.String("l", "", "Limit hosts")
	fs.Parse(args)

	ansibleBin, env, keyPath := setupAnsibleEnv()
	if ansibleBin == "" {
		return
	}

	fmt.Printf("[PACKAGE] Launching OS update (dist-upgrade). Forks: %d\n", *forksPtr)
	os.WriteFile("/os_update.yml", []byte(OSUpdateYml), 0644)

	cmdArgs := []string{
		"-i", "/inventory.yaml",
		"--private-key", keyPath,
		"/os_update.yml",
		"-f", fmt.Sprintf("%d", *forksPtr),
	}
	if *limitPtr != "" {
		cmdArgs = append(cmdArgs, "-l", *limitPtr)
	}

	if err := runAnsible(ansibleBin, env, cmdArgs); err != nil {
		fmt.Printf("[ERROR] Update error: %v\n", err)
	} else {
		fmt.Println("[+OK+] System successfully updated!")
	}
}

func runFlux() {
	fmt.Println("[GO] Flux Bootstrap...")
	fluxBin := findBinary("flux")
	if fluxBin == "" {
		fmt.Println("[ERROR] flux not found")
		return
	}
	cmdArgs := []string{"bootstrap", "github", "--token-auth", "--owner=featsci", "--repository=cli", "--branch=main", "--path=flux/test", "--private", "--kubeconfig=/kubeconfig"}
	cmd := exec.Command(fluxBin, cmdArgs...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("[ERROR] Flux error: %v\n", err)
	} else {
		fmt.Println("[+OK+] Flux Done!")
	}
}

func runKubespraySmart(args []string) {
	fs := flag.NewFlagSet("kubespray", flag.ContinueOnError)
	forksPtr := fs.Int("f", 5, "Forks")
	limitPtr := fs.String("l", "", "Limit hosts")
	fs.Parse(args)

	ansibleBin, env, keyPath := setupAnsibleEnv()
	if ansibleBin == "" {
		return
	}

	workDir := "/"
	for _, d := range []string{"/kubespray", "/runner/project", "/"} {
		if _, err := os.Stat(filepath.Join(d, "cluster.yml")); err == nil {
			workDir = d
			break
		}
	}
	os.Chdir(workDir)
	fmt.Printf("[FILE_FOLDER] Working directory: %s\n", workDir)

	hasBundle := false
	if _, err := os.Stat("/root/k8s_images.tar"); err == nil {
		hasBundle = true
	}

	extraVars := []string{
		"-e", `{"download_run_once":false,"download_localhost":false,"download_force_cache":false}`,
	}

	if hasBundle {
		fmt.Println("[PACKAGE] Offline Bundle detected. Launching in 3 stages for speed...")

		fmt.Println("\n>>> [STAGE 1/3] Installing Container Engine...")
		cmd1Args := append([]string{"-i", "/inventory.yaml", "--private-key", keyPath, "cluster.yml", "--tags=container-engine", "-f", fmt.Sprintf("%d", *forksPtr)}, extraVars...)
		if *limitPtr != "" {
			cmd1Args = append(cmd1Args, "--limit", *limitPtr)
		}
		runAnsible(ansibleBin, env, cmd1Args)

		fmt.Println("\n>>> [STAGE 2/3] Importing images from bundle (Internal Network)...")
		os.WriteFile("/load_images.yml", []byte(LoadImagesYml), 0644)
		cmd2Args := append([]string{"-i", "/inventory.yaml", "--private-key", keyPath, "/load_images.yml", "-f", fmt.Sprintf("%d", *forksPtr)}, extraVars...)
		if *limitPtr != "" {
			cmd2Args = append(cmd2Args, "--limit", *limitPtr)
		}
		if err := runAnsible(ansibleBin, env, cmd2Args); err != nil {
			fmt.Printf("[WARNING] Image import error: %v\n", err)
		}

		fmt.Println("\n>>> [STAGE 3/3] Finalizing cluster...")
		cmd3Args := append([]string{"-i", "/inventory.yaml", "--private-key", keyPath, "cluster.yml", "-f", fmt.Sprintf("%d", *forksPtr)}, extraVars...)
		if *limitPtr != "" {
			cmd3Args = append(cmd3Args, "--limit", *limitPtr)
		}
		if err := runAnsible(ansibleBin, env, cmd3Args); err != nil {
			fmt.Printf("[ERROR] Kubespray error: %v\n", err)
			debugShell(env)
		} else {
			fmt.Println("\n[+OK+][+OK+][+OK+] Cluster successfully deployed (Offline Mode)!")
		}
	} else {
		fmt.Println("🌐 Standard mode (Online Download)...")
		cmdArgs := append([]string{"-i", "/inventory.yaml", "--private-key", keyPath, "cluster.yml"}, extraVars...)
		if *forksPtr > 0 {
			cmdArgs = append(cmdArgs, "-f", fmt.Sprintf("%d", *forksPtr))
		}
		if *limitPtr != "" {
			cmdArgs = append(cmdArgs, "--limit", *limitPtr)
		}
		if err := runAnsible(ansibleBin, env, cmdArgs); err != nil {
			fmt.Printf("[ERROR] Kubespray error: %v\n", err)
			debugShell(env)
		} else {
			fmt.Println("\n[+OK+][+OK+][+OK+] Kubespray successfully finished!")
		}
	}
}

func runRemoveNode(args []string) {
	fs := flag.NewFlagSet("remove-node", flag.ContinueOnError)
	nodeNamePtr := fs.String("node", "", "Node name to remove")
	resetPtr := fs.Bool("reset", true, "Reset node (drain/delete)")
	ungracefulPtr := fs.Bool("ungraceful", false, "Allow ungraceful removal")
	fs.Parse(args)

	if *nodeNamePtr == "" {
		fmt.Println("[ERROR] Error: Node name not specified (-node)")
		return
	}

	ansibleBin, env, keyPath := setupAnsibleEnv()
	if ansibleBin == "" {
		return
	}

	workDir := "/"
	for _, d := range []string{"/kubespray", "/runner/project", "/"} {
		if _, err := os.Stat(filepath.Join(d, "remove-node.yml")); err == nil {
			workDir = d
			break
		}
	}
	os.Chdir(workDir)

	cmdArgs := []string{
		"-i", "/inventory.yaml",
		"--private-key", keyPath,
		"remove-node.yml",
		"-e", fmt.Sprintf("node=%s", *nodeNamePtr),
	}
	if !*resetPtr {
		cmdArgs = append(cmdArgs, "-e", "reset_nodes=false")
	}
	if *ungracefulPtr {
		cmdArgs = append(cmdArgs, "-e", "allow_ungraceful_removal=true")
	}
	cmdArgs = append(cmdArgs, "--become", "--become-user=root")

	fmt.Printf("[TRASH_CAN] Launching Graceful Removal for node: %s\n", *nodeNamePtr)

	if err := runAnsible(ansibleBin, env, cmdArgs); err != nil {
		fmt.Printf("\n[ERROR] remove-node error: %v\n", err)
	} else {
		fmt.Println("\n[+OK+] Node successfully removed from cluster.")
	}
}

func runDiskSetup(args []string) {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	_ = fs.Int("f", 5, "Forks")
	limitPtr := fs.String("l", "", "Limit hosts")
	fs.Parse(args)

	ansibleBin, env, keyPath := setupAnsibleEnv()
	if ansibleBin == "" {
		return
	}

	fmt.Println("[DISK/STORAGE] [TASK] Disk configuration (Mount only)...")
	os.WriteFile("/setup-disk.yml", []byte(DiskSetupTaskYml), 0644)
	os.WriteFile("/prepare-disks.yml", []byte(DiskSetupMainYml), 0644)

	cmdArgs := []string{"-i", "/inventory.yaml", "--private-key", keyPath, "/prepare-disks.yml"}

	if *limitPtr != "" {
		cmdArgs = append(cmdArgs, "--limit", *limitPtr)
	}

	if err := runAnsible(ansibleBin, env, cmdArgs); err != nil {
		fmt.Printf("[ERROR] Error: %v\n", err)
	} else {
		fmt.Println("[+OK+] Disks configured.")
	}
}

func runAnsible(bin string, env []string, args []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func downloadFile(url, filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

func setupAnsibleEnv() (string, []string, string) {
	keyPath := "/root/.ssh/id_rsa"
	if _, err := os.Stat(keyPath); err == nil {
		os.Chmod(keyPath, 0600)
	}
	fmt.Println("[MAGNIFYING_GLASS] Searching for ansible-playbook...")
	ansibleBin := findBinary("ansible-playbook")
	if ansibleBin == "" {
		filepath.Walk("/usr", func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && info.Name() == "ansible-playbook" {
				ansibleBin = path
				return io.EOF
			}
			return nil
		})
	}
	if ansibleBin == "" {
		fmt.Println("[ERROR] ansible-playbook not found.")
		debugShell(nil)
		return "", nil, ""
	}
	newPath := filepath.Dir(ansibleBin) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if old := os.Getenv("PATH"); old != "" {
		newPath = filepath.Dir(ansibleBin) + ":" + old
	}
	env := []string{"PATH=" + newPath, "HOME=/root", "TERM=xterm", "ANSIBLE_HOST_KEY_CHECKING=False", "ANSIBLE_FORCE_COLOR=true"}
	return ansibleBin, env, keyPath
}

func findBinary(name string) string {
	for _, p := range []string{"/usr/share/kubespray/venv/bin/" + name, "/venv/bin/" + name, "/usr/local/bin/" + name, "/usr/bin/" + name, "/bin/" + name} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func debugShell(env []string) {
	fmt.Println("[WARNING] Debug Shell. Type 'exit'.")
	sh := exec.Command("/bin/bash")
	if env != nil {
		sh.Env = env
	} else {
		sh.Env = os.Environ()
	}
	sh.Stdin = os.Stdin
	sh.Stdout = os.Stdout
	sh.Stderr = os.Stderr
	sh.Run()
}

func pullAndUnpack(imgRef, dest string) error {
	ref, err := name.ParseReference(imgRef)
	if err != nil {
		return err
	}
	img, err := remote.Image(ref)
	if err != nil {
		return err
	}
	tarStream := mutate.Extract(img)
	defer tarStream.Close()
	tr := tar.NewReader(tarStream)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		target := filepath.Join(dest, h.Name)
		if h.Typeflag == tar.TypeDir {
			os.MkdirAll(target, 0755)
			continue
		}
		if h.Typeflag == tar.TypeReg {
			os.MkdirAll(filepath.Dir(target), 0755)
			f, _ := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(h.Mode))
			io.Copy(f, tr)
			f.Close()
		}
		if h.Typeflag == tar.TypeSymlink {
			os.MkdirAll(filepath.Dir(target), 0755)
			os.Remove(target)
			os.Symlink(h.Linkname, target)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	io.Copy(d, s)
	return nil
}
