import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: 'category',
      label: 'Getting Started',
      collapsed: false,
      items: [
        'intro',
        'quickstart',
        'installation',
        'cron-expressions',
        'cli-commands',
      ],
    },
    {
      type: 'category',
      label: 'Core Concepts & Architecture',
      collapsed: false,
      items: [
        'architecture/advisory-locks',
        'architecture/leader-election',
        'architecture/fencing-tokens',
        'architecture/clock-engine',
      ],
    },
    {
      type: 'category',
      label: 'Feature Guides',
      collapsed: false,
      items: [
        'guides/execution-safety',
        'guides/overlap-missed-runs',
        'guides/observability',
        'guides/history-store',
        'guides/admin-api',
        'guides/user-fencing',
      ],
    },
    {
      type: 'category',
      label: 'Production & Integrations',
      collapsed: false,
      items: [
        'production/pgbouncer',
        'production/kubernetes',
        'production/pgx-driver',
      ],
    },
    {
      type: 'category',
      label: 'API Reference',
      collapsed: false,
      items: [
        'api/dcron',
        'api/executor',
        'api/metrics',
      ],
    },
    {
      type: 'category',
      label: 'Help & Community',
      collapsed: false,
      items: [
        'troubleshooting',
        'contributing',
        'faq',
      ],
    },
  ],
};

export default sidebars;
