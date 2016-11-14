package cloudup

import (
	"fmt"
	channelsapi "k8s.io/kops/channels/pkg/api"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/fitasks"
	"k8s.io/kops/upup/pkg/fi/utils"
)

type BootstrapChannelBuilder struct {
	cluster *kops.Cluster
}

var _ TaskBuilder = &BootstrapChannelBuilder{}

func (b *BootstrapChannelBuilder) BuildTasks(l *Loader) error {
	addons := b.buildManifest()
	addonsYAML, err := utils.YamlMarshal(addons)
	if err != nil {
		return fmt.Errorf("error serializing addons yaml: %v", err)
	}

	name := b.cluster.Name + "-addons-bootstrap"

	l.tasks[name] = &fitasks.ManagedFile{
		Name:     fi.String(name),
		Location: fi.String("addons/bootstrap-channel.yaml"),
		Contents: fi.WrapResource(fi.NewBytesResource(addonsYAML)),
	}

	return nil
}

func (b *BootstrapChannelBuilder) buildManifest() *channelsapi.Addons {
	addons := &channelsapi.Addons{}
	addons.Kind = "Addons"
	addons.Name = "bootstrap"

	addons.Spec.Addons = append(addons.Spec.Addons, &channelsapi.AddonSpec{
		Name:     fi.String("kube-dns"),
		Version:  fi.String("1.4.0"),
		Selector: map[string]string{"k8s-addon": "kube-dns.addons.k8s.io"},
		Manifest: fi.String("kube-dns/v1.4.0.yaml"),
	})

	addons.Spec.Addons = append(addons.Spec.Addons, &channelsapi.AddonSpec{
		Name:     fi.String("core"),
		Version:  fi.String("1.4.0"),
		Selector: map[string]string{"k8s-addon": "core.addons.k8s.io"},
		Manifest: fi.String("core/v1.4.0.yaml"),
	})

	addons.Spec.Addons = append(addons.Spec.Addons, &channelsapi.AddonSpec{
		Name:     fi.String("dns-controller"),
		Version:  fi.String("1.4.1"),
		Selector: map[string]string{"k8s-addon": "dns-controller.addons.k8s.io"},
		Manifest: fi.String("dns-controller/v1.4.1.yaml"),
	})

	if b.cluster.Spec.Networking.VXLAN != nil {
		addons.Spec.Addons = append(addons.Spec.Addons, &channelsapi.AddonSpec{
			Name:     fi.String("krouton"),
			Version:  fi.String("1.0.0"),
			Selector: map[string]string{"k8s-addon": "krouton.addons.k8s.io"},
			// TODO: Replace with real version once it merges or embed?
			Manifest: fi.String("https://raw.githubusercontent.com/kopeio/krouton/4dd0f75871518e875e06a5250dd45d831d6111a2/k8s/krouton.yaml"),
		})

		// TODO: Create configuration object (maybe create it but orphan it)?
	}

	return addons
}
