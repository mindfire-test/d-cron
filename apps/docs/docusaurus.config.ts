import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'd-cron',
  tagline: 'Distributed Cron Scheduler for Horizontally-Scaled Go Applications',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  url: 'https://mindfiredigital.github.io',
  baseUrl: '/d-cron/',

  organizationName: 'mindfiredigital',
  projectName: 'd-cron',

  onBrokenLinks: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/mindfire-test/d-cron/tree/main/apps/docs/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themes: [
    [
      require.resolve('@easyops-cn/docusaurus-search-local'),
      {
        hashed: true,
        language: ['en'],
        highlightSearchTermsOnTargetPage: true,
        explicitSearchResultPath: true,
        docsRouteBasePath: '/docs',
      },
    ],
  ],

  themeConfig: {
    image: 'img/docusaurus-social-card.jpg',
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'd-cron',
      logo: {
        alt: 'd-cron Logo',
        src: 'img/logo.svg',
      },
      items: [
        {
          to: '/docs/intro',
          label: 'Docs',
          position: 'left',
        },
        {
          to: '/docs/architecture/advisory-locks',
          label: 'Architecture',
          position: 'left',
        },
        {
          to: '/docs/guides/observability',
          label: 'Guides',
          position: 'left',
        },
        {
          to: '/docs/api/dcron',
          label: 'API Reference',
          position: 'left',
        },
        {
          to: '/docs/faq',
          label: 'FAQ & Comparison',
          position: 'left',
        },
        {
          type: 'docsVersionDropdown',
          position: 'right',
        },
        {
          href: 'https://github.com/mindfire-test/d-cron',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Documentation',
          items: [
            {
              label: 'Getting Started',
              to: '/docs/intro',
            },
            {
              label: 'Quickstart',
              to: '/docs/quickstart',
            },
            {
              label: 'Architecture & Locking',
              to: '/docs/architecture/advisory-locks',
            },
            {
              label: 'API Reference',
              to: '/docs/api/dcron',
            },
          ],
        },
        {
          title: 'Features & Production',
          items: [
            {
              label: 'Observability & Dashboard',
              to: '/docs/guides/observability',
            },
            {
              label: 'Execution History',
              to: '/docs/guides/history-store',
            },
            {
              label: 'PgBouncer & Connection Pools',
              to: '/docs/production/pgbouncer',
            },
            {
              label: 'Kubernetes Probes',
              to: '/docs/production/kubernetes',
            },
          ],
        },
        {
          title: 'Community & Source',
          items: [
            {
              label: 'GitHub Repository',
              href: 'https://github.com/mindfire-test/d-cron',
            },
            {
              label: 'Contributor Guide',
              to: '/docs/contributing',
            },
            {
              label: 'Mindfire Digital',
              href: 'https://github.com/mindfiredigital',
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Mindfire Digital. Built with Docusaurus for d-cron.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'yaml', 'sql', 'go', 'promql'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
