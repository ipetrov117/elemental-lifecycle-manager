/*
Copyright © 2026 SUSE LLC
SPDX-License-Identifier: Apache-2.0

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

package upgrade

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/suse/elemental/v3/pkg/manifest/api"
	"github.com/suse/elemental/v3/pkg/manifest/api/core"
	"github.com/suse/elemental/v3/pkg/manifest/api/solution"
	"github.com/suse/elemental/v3/pkg/manifest/resolver"
	"k8s.io/apimachinery/pkg/types"
)

const (
	releaseVersion   = "1.0.0"
	releaseName      = "release"
	releaseNamespace = "release-namespace"
	baseOSImage      = "registry.com/foo:16.0"
	baseOSVersion    = "16.0"
	k8sVersion       = "1.35.1"
	k8sImage         = "registry.com/rke2-tar:1.35.1"
	lcmChartName     = "elemental-lifecycle-manager"
	lcmCRDChartName  = "elemental-lifecycle-manager-crds"
)

var (
	coreChart            = &api.HelmChart{Chart: "foo", Repository: "foo-repo"} //nolint:goconst
	solutionChart        = &api.HelmChart{Chart: "bar", Repository: "bar-repo"} //nolint:goconst
	coreChartRepo        = &api.HelmRepository{Name: "foo-repo", URL: "https://charts.foo.com"}
	solutionChartRepo    = &api.HelmRepository{Name: "bar-repo", URL: "https://charts.bar.com"}
	corePlatformManifest = &core.ReleaseManifest{
		Components: core.Components{
			OperatingSystem: &core.OperatingSystem{
				Image: core.Image{
					Base: baseOSImage,
				},
			},
			Kubernetes: &core.Kubernetes{
				Image:   k8sImage,
				Version: k8sVersion,
			},
			Helm: &api.Helm{
				Charts:       []*api.HelmChart{coreChart},
				Repositories: []*api.HelmRepository{coreChartRepo},
			},
		},
	}
	solutionExtension = &solution.ReleaseManifest{
		Components: solution.Components{
			Helm: &api.Helm{
				Charts:       []*api.HelmChart{solutionChart},
				Repositories: []*api.HelmRepository{solutionChartRepo},
			},
		},
	}
)

var _ = Describe("Configuration creation", func() {
	It("Should fail if resolved manifest is nil", func() {
		_, err := NewConfig(nil, "", types.NamespacedName{}, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError("manifest is nil"))
	})

	It("Should fail if resolved manifest is missing core platform", func() {
		_, err := NewConfig(&resolver.ResolvedManifest{}, "", types.NamespacedName{}, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError("core platform manifest is required"))
	})

	It("Should fail for an incorrect OS image definition", func() {
		manifest := &resolver.ResolvedManifest{
			CorePlatform: &core.ReleaseManifest{
				Components: core.Components{
					OperatingSystem: &core.OperatingSystem{
						Image: core.Image{
							Base: "registry.com/foo:bar@broken@broken",
						},
					},
				},
			},
		}
		_, err := NewConfig(manifest, "", types.NamespacedName{}, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing OS image \"registry.com/foo:bar@broken@broken\": tag can only contain the characters"))
	})

	It("Should fail for unknown charts", func() {
		manifest := &resolver.ResolvedManifest{
			CorePlatform: &core.ReleaseManifest{
				Components: core.Components{
					OperatingSystem: &core.OperatingSystem{
						Image: core.Image{
							Base: baseOSImage,
						},
					},
					Kubernetes: &core.Kubernetes{
						Image:   k8sImage,
						Version: k8sVersion,
					},
					Helm: &api.Helm{
						Charts: []*api.HelmChart{
							{Chart: "chart1"},
							{Chart: "chart2"},
						},
					},
				},
			},
		}

		runtimeConfig := &RuntimeConfig{
			HelmCharts: map[string]RuntimeHelmChartConfig{
				"chart1":   {},
				"unknown1": {},
				"unknown2": {},
				"chatr2":   {}},
		}

		_, err := NewConfig(manifest, "", types.NamespacedName{}, runtimeConfig)
		Expect(err).To(HaveOccurred())

		errSubstr := "Unknown chart references: "
		Expect(err.Error()).To(ContainSubstring(errSubstr))
		_, after, _ := strings.Cut(err.Error(), errSubstr)
		unknownCharts := strings.Split(after, ", ")
		Expect(unknownCharts).To(ConsistOf("unknown1", "unknown2", "chatr2"))
	})

	It("Should construct a correct upgrade configuration", func() {
		resolvedManifest := &resolver.ResolvedManifest{CorePlatform: corePlatformManifest, SolutionExtension: solutionExtension}
		runtimeSecretValues := &RuntimeSecretValueSource{
			Name: "foo-secret",
			Keys: []string{"foo-values"},
		}
		runtimeConfig := &RuntimeConfig{
			DrainOpts: &DrainOpts{ControlPlane: false, Worker: true},
			HelmCharts: map[string]RuntimeHelmChartConfig{
				coreChart.Chart: {
					ValuesFrom: RuntimeHelmChartValuesFrom{SecretRef: runtimeSecretValues},
				},
			},
		}
		releaseNN := types.NamespacedName{Name: releaseName, Namespace: releaseNamespace}

		config, err := NewConfig(resolvedManifest, releaseVersion, releaseNN, runtimeConfig)
		Expect(err).ToNot(HaveOccurred())

		// release validation
		Expect(config.ReleaseVersion).To(Equal(releaseVersion))
		Expect(config.ReleaseNamespacedName).To(Equal(releaseNN))

		// OS validation
		osConfig := config.OS
		Expect(osConfig.DrainOpts).To(Equal(runtimeConfig.DrainOpts))
		Expect(osConfig.Image).To(Equal(baseOSImage))
		Expect(osConfig.Version).To(Equal(baseOSVersion))

		// K8s validation
		k8sConfig := config.Kubernetes
		Expect(k8sConfig.DrainOpts).To(Equal(runtimeConfig.DrainOpts))
		Expect(k8sConfig.Image).To(Equal(k8sImage))
		Expect(k8sConfig.Version).To(Equal(k8sVersion))

		// Helm validation
		Expect(config.HelmCharts).To(HaveLen(2))

		coreHelmChartConfig := &HelmChartConfig{
			Chart:      coreChart,
			Repository: coreChartRepo,
			RuntimeConfig: RuntimeHelmChartConfig{
				ValuesFrom: RuntimeHelmChartValuesFrom{
					SecretRef: runtimeSecretValues,
				},
			},
		}
		Expect(config.HelmCharts[0]).To(Equal(coreHelmChartConfig))

		solutionHelmChartConfig := &HelmChartConfig{
			Chart:      solutionChart,
			Repository: solutionChartRepo,
		}
		Expect(config.HelmCharts[1]).To(Equal(solutionHelmChartConfig))
	})
})

//nolint:goconst
var _ = Describe("Helm chart selection", func() {
	DescribeTable("FilterWithDependencies",
		func(charts HelmChartConfigs, choose func(*HelmChartConfig) bool, expected []string) {
			selected, err := charts.FilterWithDependencies(choose)

			Expect(err).NotTo(HaveOccurred())
			Expect(chartNames(selected)).To(Equal(expected))
		},
		Entry("when no charts are selected",
			HelmChartConfigs{standardChart("foo"), standardChart("bar")},
			selectByName(),
			[]string{},
		),
		Entry("when charts and dependencies are selected",
			HelmChartConfigs{standardChart("foo", "bar"), standardChart("bar")},
			selectAll,
			[]string{"bar", "foo"},
		),
		Entry("when a chart with dependency is selected",
			HelmChartConfigs{standardChart("foo", "bar"), standardChart("bar"), standardChart("baz")},
			selectByName("foo"),
			[]string{"bar", "foo"},
		),
		Entry("when a chart with transitive dependency is selected",
			HelmChartConfigs{standardChart("foo", "bar"), standardChart("bar", "baz"), standardChart("baz"), standardChart("pippo")},
			selectByName("foo"),
			[]string{"baz", "bar", "foo"},
		),
		Entry("when selected charts share a dependency",
			HelmChartConfigs{standardChart("foo", "baz"), standardChart("bar", "baz"), standardChart("baz"), standardChart("pippo")},
			selectAll,
			[]string{"baz", "foo", "bar", "pippo"},
		),
		Entry("when dependency is not a chart",
			HelmChartConfigs{
				{
					Chart: &api.HelmChart{
						Chart:     "foo",
						DependsOn: []api.HelmChartDependency{{Name: "bar", Type: api.DependencyTypeExtension}},
					},
				},
				standardChart("bar"),
			},
			selectByName("foo"),
			[]string{"foo"},
		),
		Entry("when dependency is missing",
			HelmChartConfigs{standardChart("foo", "missing")},
			selectAll,
			[]string{"foo"},
		),
	)

	It("should fail on circular dependencies", func() {
		charts := HelmChartConfigs{priorityChart("foo", "bar"), standardChart("bar", "foo")}

		selected, err := charts.FilterWithDependencies(selectByName("foo"))
		Expect(err).To(MatchError("circular dependency detected involving chart foo"))
		Expect(selected).To(BeNil())

		_, err = charts.Priority()
		Expect(err).To(MatchError("circular dependency detected involving chart foo"))

		_, err = charts.Standard()
		Expect(err).To(MatchError("circular dependency detected involving chart bar"))
	})

	DescribeTable("Runtime priority configuration",
		func(chartPriority bool, runtimePriority *bool, expectedPriority, expectedStandard []string) {
			c := newChartConfig("foo", chartPriority)
			c.RuntimeConfig.PriorityUpgrade = runtimePriority
			charts := HelmChartConfigs{c}

			Expect(charts.Priority()).To(WithTransform(chartNames, Equal(expectedPriority)))
			Expect(charts.Standard()).To(WithTransform(chartNames, Equal(expectedStandard)))
		},
		Entry("when priority chart is not edited at runtime", true, nil, []string{"foo"}, []string{}),
		Entry("when standard chart is not edited at runtime", false, nil, []string{}, []string{"foo"}),
		Entry("when priority chart is overridden at runtime", true, new(false), []string{}, []string{"foo"}),
		Entry("when stamdard chart is overridden at runtime", false, new(true), []string{"foo"}, []string{}),
	)

	It("should include dependencies across different chart types", func() {
		charts := HelmChartConfigs{
			priorityChart("foo", "bar"),
			standardChart("bar"),
			standardChart("baz", "pippo"),
			priorityChart("pippo"),
		}

		priority, err := charts.Priority()
		Expect(err).NotTo(HaveOccurred())
		Expect(chartNames(priority)).To(Equal([]string{"bar", "foo", "pippo"}))

		standard, err := charts.Standard()
		Expect(err).NotTo(HaveOccurred())
		Expect(chartNames(standard)).To(Equal([]string{"bar", "pippo", "baz"}))
	})

	It("should exclude LCM charts from priority/standard chart outputs", func() {
		charts := HelmChartConfigs{
			priorityChart(lcmCRDChartName),
			standardChart(lcmChartName),
			priorityChart("foo", lcmChartName),
			standardChart("bar", lcmCRDChartName),
		}

		priority, err := charts.Priority()
		Expect(err).NotTo(HaveOccurred())
		Expect(chartNames(priority)).To(Equal([]string{"foo"}))

		standard, err := charts.Standard()
		Expect(err).NotTo(HaveOccurred())
		Expect(chartNames(standard)).To(Equal([]string{"bar"}))
	})

	It("should select only LCM charts", func() {
		charts := HelmChartConfigs{
			priorityChart("foo"),
			priorityChart(lcmChartName, lcmCRDChartName),
			standardChart("bar"),
			standardChart(lcmCRDChartName),
		}

		lcm, err := charts.LCM()

		Expect(err).NotTo(HaveOccurred())
		Expect(chartNames(lcm)).To(Equal([]string{lcmCRDChartName, lcmChartName}))
	})
})

func newChartConfig(name string, priority bool, helmDeps ...string) *HelmChartConfig {
	deps := make([]api.HelmChartDependency, len(helmDeps))
	for i, dep := range helmDeps {
		deps[i] = api.HelmChartDependency{Name: dep, Type: api.DependencyTypeHelm}
	}
	return &HelmChartConfig{Chart: &api.HelmChart{Chart: name, PriorityUpgrade: priority, DependsOn: deps}}
}

func priorityChart(name string, helmDeps ...string) *HelmChartConfig {
	return newChartConfig(name, true, helmDeps...)
}

func standardChart(name string, helmDeps ...string) *HelmChartConfig {
	return newChartConfig(name, false, helmDeps...)
}

func chartNames(charts HelmChartConfigs) []string {
	names := make([]string, 0, len(charts))
	for _, c := range charts {
		names = append(names, c.Chart.GetName())
	}
	return names
}

func selectAll(*HelmChartConfig) bool { return true }

func selectByName(names ...string) func(*HelmChartConfig) bool {
	return func(c *HelmChartConfig) bool { return slices.Contains(names, c.Chart.GetName()) }
}
