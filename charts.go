// Package wpfleet holds what the wpfleet binary embeds from the repository
// root: the WordPress chart that every site release is rendered from.
package wpfleet

import "embed"

// Chart is the WordPress chart, rooted at charts/wordpress. The all: prefix
// keeps files that start with an underscore, such as templates/_helpers.tpl.
//
//go:embed all:charts/wordpress
var Chart embed.FS

// ChartRoot is the directory of the chart inside Chart.
const ChartRoot = "charts/wordpress"
