# Run OpenCodeReview on ARC-managed runners

Run OpenCodeReview through its GitHub Actions integration on self-hosted Kubernetes runners managed by Actions Runner Controller in the Kelos cluster. This adds a runner operator but reuses upstream PR publishing and artifact handling rather than implementing them for direct Kubernetes Jobs. Runners connect outbound to GitHub; the cluster requires no inbound internet endpoint.
