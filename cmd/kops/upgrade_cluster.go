package main

import (
	"fmt"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"k8s.io/kops/upup/pkg/api"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup"
	"k8s.io/kops/util/pkg/tables"
	"os"
)

const DefaultChannel = "https://raw.githubusercontent.com/kubernetes/kops/master/channels/stable.yaml"

type UpgradeClusterCmd struct {
	Yes bool

	Channel string
}

var upgradeCluster UpgradeClusterCmd

func init() {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Upgrade cluster",
		Long:  `Upgrades a k8s cluster.`,
		Run: func(cmd *cobra.Command, args []string) {
			err := upgradeCluster.Run(args)
			if err != nil {
				exitWithError(err)
			}
		},
	}

	cmd.Flags().BoolVar(&upgradeCluster.Yes, "yes", false, "Apply update")
	cmd.Flags().StringVar(&upgradeCluster.Channel, "channel", DefaultChannel, "Channel to use for upgrade")

	upgradeCmd.AddCommand(cmd)
}

type upgradeAction struct {
	Item     string
	Property string
	Old      string
	New      string

	apply func()
}

func (c *UpgradeClusterCmd) Run(args []string) error {
	err := rootCommand.ProcessArgs(args)
	if err != nil {
		return err
	}

	clusterRegistry, cluster, err := rootCommand.Cluster()
	if err != nil {
		return err
	}

	instanceGroupRegistry, err := rootCommand.InstanceGroupRegistry()
	if err != nil {
		return err
	}

	instanceGroups, err := instanceGroupRegistry.ReadAll()

	if cluster.Annotations[api.AnnotationNameManagement] == api.AnnotationValueManagementImported {
		return fmt.Errorf("upgrade is not for use with imported clusters (did you mean `kops toolbox convert-imported`?)")
	}

	channel, err := api.LoadChannel(c.Channel)
	if err != nil {
		return fmt.Errorf("error loading channel %q: %v", c.Channel, err)
	}

	channelClusterSpec := channel.Spec.Cluster
	if channelClusterSpec == nil {
		// Just to prevent too much nil handling
		channelClusterSpec = &api.ClusterSpec{}
	}

	//latestKubernetesVersion, err := api.FindLatestKubernetesVersion()
	//if err != nil {
	//	return err
	//}

	var actions []*upgradeAction
	if channelClusterSpec.KubernetesVersion != "" && cluster.Spec.KubernetesVersion != channelClusterSpec.KubernetesVersion {
		actions = append(actions, &upgradeAction{
			Item:     "Cluster",
			Property: "KubernetesVersion",
			Old:      cluster.Spec.KubernetesVersion,
			New:      channelClusterSpec.KubernetesVersion,
			apply: func() {
				cluster.Spec.KubernetesVersion = channelClusterSpec.KubernetesVersion
			},
		})
	}

	// Prompt to upgrade addins?

	// Prompt to upgrade to kubenet
	if channelClusterSpec.Networking != nil {
		clusterNetworking := cluster.Spec.Networking
		if clusterNetworking == nil {
			clusterNetworking = &api.NetworkingSpec{}
		}
		// TODO: make this less hard coded
		if channelClusterSpec.Networking.Kubenet != nil && channelClusterSpec.Networking.Classic != nil {
			actions = append(actions, &upgradeAction{
				Item:     "Cluster",
				Property: "Networking",
				Old:      "classic",
				New:      "kubenet",
				apply: func() {
					cluster.Spec.Networking.Classic = nil
					cluster.Spec.Networking.Kubenet = channelClusterSpec.Networking.Kubenet
				},
			})
		}
	}

	cloud, err := cloudup.BuildCloud(cluster)
	if err != nil {
		return err
	}

	// Prompt to upgrade image
	{
		var matches []*api.ChannelImageSpec
		for _, image := range channel.Spec.Images {
			cloudProvider := image.Labels[api.ImageLabelCloudprovider]
			if cloudProvider != string(cloud.ProviderID()) {
				continue
			}
			matches = append(matches, image)
		}

		if len(matches) == 0 {
			glog.Warningf("No matching images specified in channel; cannot prompt for upgrade")
		} else if len(matches) != 1 {
			glog.Warningf("Multiple matching images specified in channel; cannot prompt for upgrade")
		} else {
			for _, ig := range instanceGroups {
				if ig.Spec.Image != matches[0].Name {
					target := ig
					actions = append(actions, &upgradeAction{
						Item:     "InstanceGroup/" + target.Name,
						Property: "Image",
						Old:      target.Spec.Image,
						New:      matches[0].Name,
						apply: func() {
							target.Spec.Image = matches[0].Name
						},
					})
				}
			}
		}
	}

	// Prompt to upgrade to overlayfs
	if channelClusterSpec.Docker != nil {
		if cluster.Spec.Docker == nil {
			cluster.Spec.Docker = &api.DockerConfig{}
		}
		// TODO: make less hard-coded
		if channelClusterSpec.Docker.Storage != nil {
			dockerStorage := fi.StringValue(cluster.Spec.Docker.Storage)
			if dockerStorage != fi.StringValue(channelClusterSpec.Docker.Storage) {
				actions = append(actions, &upgradeAction{
					Item:     "Cluster",
					Property: "Docker.Storage",
					Old:      dockerStorage,
					New:      fi.StringValue(channelClusterSpec.Docker.Storage),
					apply: func() {
						cluster.Spec.Docker.Storage = channelClusterSpec.Docker.Storage
					},
				})
			}
		}
	}

	if len(actions) == 0 {
		// TODO: Allow --force option to force even if not needed?
		// Note stderr - we try not to print to stdout if no update is needed
		fmt.Fprintf(os.Stderr, "\nNo upgrade required\n")
		return nil
	}

	{
		t := &tables.Table{}
		t.AddColumn("ITEM", func(a *upgradeAction) string {
			return a.Item
		})
		t.AddColumn("PROPERTY", func(a *upgradeAction) string {
			return a.Property
		})
		t.AddColumn("OLD", func(a *upgradeAction) string {
			return a.Old
		})
		t.AddColumn("NEW", func(a *upgradeAction) string {
			return a.New
		})

		err := t.Render(actions, os.Stdout, "ITEM", "PROPERTY", "OLD", "NEW")
		if err != nil {
			return err
		}
	}

	if !c.Yes {
		fmt.Printf("\nMust specify --yes to perform upgrade\n")
		return nil
	} else {
		for _, action := range actions {
			action.apply()
		}

		// TODO: DRY this chunk
		err = cluster.PerformAssignments()
		if err != nil {
			return fmt.Errorf("error populating configuration: %v", err)
		}

		fullCluster, err := cloudup.PopulateClusterSpec(cluster, clusterRegistry)
		if err != nil {
			return err
		}

		err = api.DeepValidate(fullCluster, instanceGroups, true)
		if err != nil {
			return err
		}

		// Note we perform as much validation as we can, before writing a bad config
		err = clusterRegistry.Update(cluster)
		if err != nil {
			return err
		}

		for _, g := range instanceGroups {
			err := instanceGroupRegistry.Update(g)
			if err != nil {
				return fmt.Errorf("error writing InstanceGroup %q to registry: %v", g.Name, err)
			}
		}

		err = clusterRegistry.WriteCompletedConfig(fullCluster)
		if err != nil {
			return fmt.Errorf("error writing completed cluster spec: %v", err)
		}

		fmt.Printf("\nUpdates applied to configuration.\n")

		// TODO: automate this step
		fmt.Printf("You can now apply these changes, using `kops update cluster %s`\n", cluster.Name)
	}

	return nil
}
