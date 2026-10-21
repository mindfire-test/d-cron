import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: 'doc',
      id: 'intro',
      label: 'Overview',
    },
    {
      type: 'doc',
      id: 'installation',
      label: 'Installation',
    },
    {
      type: 'doc',
      id: 'quickstart',
      label: 'Usage Guide',
    },
    {
      type: 'category',
      label: 'Features',
      collapsed: false,
      link: {
        type: 'generated-index',
        title: 'Features',
        description: 'Explore the core features of d-cron: distributed coordination, split-brain fencing, execution safety, and observability.',
        slug: '/features',
      },
      items: [
        {
          type: 'doc',
          id: 'architecture/advisory-locks',
          label: 'Core Architecture',
        },
        {
          type: 'doc',
          id: 'architecture/fencing-tokens',
          label: 'Split-Brain & Fencing',
        },
        {
          type: 'doc',
          id: 'guides/execution-safety',
          label: 'Execution Safety & Retries',
        },
        {
          type: 'doc',
          id: 'guides/overlap-missed-runs',
          label: 'Overlap & Missed Runs',
        },
        {
          type: 'doc',
          id: 'guides/history-store',
          label: 'Execution History Store',
        },
        {
          type: 'doc',
          id: 'guides/observability',
          label: 'Observability & Dashboard',
        },
      ],
    },
    {
      type: 'doc',
      id: 'production/kubernetes',
      label: 'Production & Kubernetes',
    },
    {
      type: 'doc',
      id: 'api/dcron',
      label: 'API Reference',
    },
    {
      type: 'doc',
      id: 'troubleshooting',
      label: 'Troubleshooting & FAQ',
    },
    {
      type: 'doc',
      id: 'contributing',
      label: 'Contributing',
    },
  ],
};

export default sidebars;
