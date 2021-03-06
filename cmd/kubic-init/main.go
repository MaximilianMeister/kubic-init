package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/renstrom/dedent"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	utilflag "k8s.io/apiserver/pkg/util/flag"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/validation"
	kubeadmcmd "k8s.io/kubernetes/cmd/kubeadm/app/cmd"
	kubeadmupcmd "k8s.io/kubernetes/cmd/kubeadm/app/cmd/upgrade"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/features"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
	kubeconfigutil "k8s.io/kubernetes/cmd/kubeadm/app/util/kubeconfig"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"

	"github.com/kubic-project/kubic-init/pkg/apis"
	kubiccluster "github.com/kubic-project/kubic-init/pkg/cluster"
	"github.com/kubic-project/kubic-init/pkg/cni"
	_ "github.com/kubic-project/kubic-init/pkg/cni/flannel"
	kubiccfg "github.com/kubic-project/kubic-init/pkg/config"
	"github.com/kubic-project/kubic-init/pkg/controller"
	"github.com/kubic-project/kubic-init/pkg/loader"
)

// to be set from the build process
var Version string
var Build string

// newCmdBootstrap returns a "kubic-init bootstrap" command.
func newCmdBootstrap(out io.Writer) *cobra.Command {
	kubicCfg := &kubiccfg.KubicInitConfiguration{}

	var kubicCfgFile string
	var skipTokenPrint = false
	var dryRun = false
	var vars = []string{}
	var postControlManifDir = kubiccfg.DefaultPostControlPlaneManifestsDir
	block := true

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap the node, either as a seeder or as a regular node depending on the 'seed' config argument.",
		Run: func(cmd *cobra.Command, args []string) {
			var err error

			kubicCfg, err = kubiccfg.ConfigFileAndDefaultsToKubicInitConfig(kubicCfgFile)
			kubeadmutil.CheckErr(err)

			err = kubicCfg.SetVars(vars)
			kubeadmutil.CheckErr(err)

			featureGates, err := features.NewFeatureGate(&features.InitFeatureGates, kubiccfg.DefaultFeatureGates)
			kubeadmutil.CheckErr(err)
			glog.V(3).Infof("[kubic] feature gates: %+v", featureGates)

			ignorePreflightErrorsSet, err := validation.ValidateIgnorePreflightErrors(kubiccfg.DefaultIgnoredPreflightErrors, false)
			kubeadmutil.CheckErr(err)

			if !kubicCfg.IsSeeder() {
				glog.V(1).Infoln("[kubic] joining the seeder at %s", kubicCfg.ClusterFormation.Seeder)
				nodeCfg, err := kubicCfg.ToNodeConfig(featureGates)
				kubeadmutil.CheckErr(err)

				joiner, err := kubeadmcmd.NewJoin("", args, nodeCfg, ignorePreflightErrorsSet)
				kubeadmutil.CheckErr(err)

				err = joiner.Run(out)
				kubeadmutil.CheckErr(err)

				glog.V(1).Infoln("[kubic] this node should have joined the cluster at this point")

			} else {
				glog.V(1).Infoln("[kubic] seeding the cluster from this node")
				masterCfg, err := kubicCfg.ToMasterConfig(featureGates)
				kubeadmutil.CheckErr(err)

				initter, err := kubeadmcmd.NewInit("", masterCfg, ignorePreflightErrorsSet, skipTokenPrint, dryRun)
				kubeadmutil.CheckErr(err)

				err = initter.Run(out)
				kubeadmutil.CheckErr(err)

				// create a kubernetes client
				// create a connection to the API server and wait for it to come up
				client, err := kubeconfigutil.ClientSetFromFile(kubeadmconstants.GetAdminKubeConfigPath())
				kubeadmutil.CheckErr(err)

				if !kubicCfg.ClusterFormation.AutoApprove {
					glog.V(1).Infoln("[kubic] removing the auto-approval rules for new nodes")
					err = kubiccluster.RemoveAutoApprovalRBAC(client)
					kubeadmutil.CheckErr(err)
				} else {
					glog.V(1).Infoln("[kubic] new nodes will be accepted automatically")
				}

				glog.V(1).Infof("[kubic] deploying CNI DaemonSet with '%s' driver", kubicCfg.Network.Cni.Driver)
				err = cni.Registry.Load(kubicCfg.Network.Cni.Driver, kubicCfg, client)
				kubeadmutil.CheckErr(err)

				kubeconfig, err := config.GetConfig()
				kubeadmutil.CheckErr(err)

				glog.V(1).Infof("[kubic] installing post-control-plane manifests")
				err = loader.InstallManifests(kubeconfig, loader.ManifestsInstallOptions{Paths: []string{postControlManifDir}})
				kubeadmutil.CheckErr(err)
			}

			if block {
				glog.V(1).Infoln("[kubic] control plane ready... looping forever")
				for {
					time.Sleep(time.Second)
				}
			}
		},
	}

	flagSet := cmd.PersistentFlags()
	flagSet.StringVar(&kubicCfgFile, "config", "",
		"Path to kubic-init config file.")
	flagSet.BoolVar(&block, "block", block, "Block after boostrapping")
	flagSet.StringSliceVar(&vars, "var", []string{}, "Set a configuration variable (ie, Network.Cni.Driver=cilium")
	// Note: All flags that are not bound to the masterCfg object should be whitelisted in cmd/kubeadm/app/apis/kubeadm/validation/validation.go

	return cmd
}

// newCmdReset returns the "kubic-init reset" command
func newCmdReset(in io.Reader, out io.Writer) *cobra.Command {
	kubicCfg := &kubiccfg.KubicInitConfiguration{}

	var kubicCfgFile string
	var vars = []string{}

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Run this to revert any changes made to this host by kubic-init.",
		Run: func(cmd *cobra.Command, args []string) {
			var err error

			kubicCfg, err = kubiccfg.ConfigFileAndDefaultsToKubicInitConfig(kubicCfgFile)
			kubeadmutil.CheckErr(err)

			err = kubicCfg.SetVars(vars)
			kubeadmutil.CheckErr(err)

			ignorePreflightErrorsSet, err := validation.ValidateIgnorePreflightErrors(kubiccfg.DefaultIgnoredPreflightErrors, false)
			kubeadmutil.CheckErr(err)

			criSocket := kubiccfg.DefaultCriSocket[kubicCfg.Runtime.Engine]
			pkiDir := kubicCfg.Certificates.Directory
			r, err := kubeadmcmd.NewReset(in, ignorePreflightErrorsSet, true, pkiDir, criSocket)
			kubeadmutil.CheckErr(err)

			err = r.Run(out)
			kubeadmutil.CheckErr(err)

			// TODO: perform any kubic-specific cleanups here
		},
	}

	flagSet := cmd.PersistentFlags()
	flagSet.StringVar(&kubicCfgFile, "config", "", "Path to kubic-init config file.")
	flagSet.StringSliceVar(&vars, "var", []string{}, "Set a configuration variable (ie, Network.Cni.Driver=cilium")

	return cmd
}

// newCmdManager runs the manager
func newCmdManager(out io.Writer) *cobra.Command {
	var kubicCfgFile string
	var kubeconfigFile = ""
	var vars = []string{}
	var crdsDir = "config/crds"
	var rbacDir = "config/rbac"

	cmd := &cobra.Command{
		Use:   "manager",
		Short: "Run the Kubic controller manager.",
		Run: func(cmd *cobra.Command, args []string) {
			var err error

			kubicCfg, err := kubiccfg.ConfigFileAndDefaultsToKubicInitConfig(kubicCfgFile)
			kubeadmutil.CheckErr(err)

			err = kubicCfg.SetVars(vars)
			kubeadmutil.CheckErr(err)

			glog.V(1).Infof("[kubic] getting a kubeconfig to talk to the apiserver")
			if len(kubeconfigFile) > 0 {
				glog.V(3).Infof("[kubic] setting KUBECONFIG to '%s'", kubeconfigFile)
				os.Setenv("KUBECONFIG", kubeconfigFile)
			}
			kubeconfig, err := config.GetConfig()
			kubeadmutil.CheckErr(err)

			glog.V(1).Infof("[kubic] creating a new manager to provide shared dependencies and start components")
			mgr, err := manager.New(kubeconfig, manager.Options{})
			kubeadmutil.CheckErr(err)

			glog.V(1).Infof("[kubic] installing components")
			err = loader.InstallRBAC(kubeconfig, loader.RBACInstallOptions{Paths: []string{rbacDir}})
			kubeadmutil.CheckErr(err)
			_, err = loader.InstallCRDs(kubeconfig, loader.CRDInstallOptions{Paths: []string{crdsDir}})
			kubeadmutil.CheckErr(err)

			glog.V(1).Infof("[kubic] setting up the scheme for all the resources")
			err = apis.AddToScheme(mgr.GetScheme())
			kubeadmutil.CheckErr(err)

			glog.V(1).Infof("[kubic] setting up all the controllers")
			err = controller.AddToManager(mgr, kubicCfg)
			kubeadmutil.CheckErr(err)

			glog.V(1).Infof("[kubic] starting the controller")
			err = mgr.Start(signals.SetupSignalHandler())
			kubeadmutil.CheckErr(err)
		},
	}

	flagSet := cmd.PersistentFlags()
	flagSet.StringVar(&kubicCfgFile, "config", "", "Path to kubic-init config file.")
	flagSet.StringVar(&kubeconfigFile, "kubeconfig", "", "Use this kubeconfig file for talking to the API server.")
	flagSet.StringSliceVar(&vars, "var", []string{}, "Set a configuration variable (ie, Network.Cni.Driver=cilium")
	flagSet.StringVar(&crdsDir, "crdsDir", crdsDir, "Load CRDs from this directory.")
	flagSet.StringVar(&rbacDir, "rbacDir", rbacDir, "Load RBACs from this directory.")
	return cmd
}

func newCmdVersion(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of kubic-init",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(out, "kubic-init version: %s (build: %s)", Version, Build)
		},
	}
	cmd.Flags().StringP("output", "o", "", "Output format; available options are 'yaml', 'json' and 'short'")
	return cmd
}

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	// see https://github.com/kubernetes/kubernetes/issues/17162#issuecomment-225596212
	flag.CommandLine.Parse([]string{})

	pflag.Set("logtostderr", "true")

	cmds := &cobra.Command{
		Use:   "kubic-init",
		Short: "kubic-init: easily bootstrap a secure Kubernetes cluster",
		Long: dedent.Dedent(`
			kubic-init: easily bootstrap a secure Kubernetes cluster.
		`),
	}

	cmds.ResetFlags()
	cmds.AddCommand(newCmdBootstrap(os.Stdout))
	cmds.AddCommand(newCmdReset(os.Stdin, os.Stdout))
	cmds.AddCommand(newCmdManager(os.Stdout))
	cmds.AddCommand(kubeadmupcmd.NewCmdUpgrade(os.Stdout))
	cmds.AddCommand(newCmdVersion(os.Stdout))

	err := cmds.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
