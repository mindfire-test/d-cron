import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import styles from './index.module.css';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={styles.heroBanner}>
      <div className={styles.heroInner}>
        <Heading as="h1" className={styles.heroTitle}>
          <span className={styles.heroTitleGradient}>{siteConfig.title}</span>
        </Heading>
        <p className={styles.heroSubtitle}>
          Distributed Cron Scheduler for Go powered by PostgreSQL Advisory Locks.
          Zero extra tables, zero daemons, 100% split-brain safe.
        </p>
        <div className={styles.buttons}>
          <Link className={styles.buttonPrimary} to="/docs/intro">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="9 18 15 12 9 6" />
            </svg>
            Get Started
          </Link>
          <a className={styles.buttonSecondary} href="https://github.com/mindfiredigital/d-cron" target="_blank" rel="noopener noreferrer">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/>
            </svg>
            GitHub
          </a>
        </div>
        <div className={styles.heroTerminal}>
          <div className={styles.terminalHeader}>
            <span className={styles.terminalDot} />
            <span className={styles.terminalDot} />
            <span className={styles.terminalDot} />
          </div>
          <div className={styles.terminalBody}>
            <span className={styles.terminalPrompt}>$</span>{' '}
            <span className={styles.terminalCommand}>go run main.go</span>
            <br />
            <span className={styles.terminalOutput}>[INFO] Connecting to PostgreSQL advisory lock engine...</span>
            <br />
            <span className={styles.terminalOutput}>[INFO] Key=84920491 TryLock() -&gt; </span>
            <span className={styles.terminalHighlight}>LEADER ELECTED</span>
            <span className={styles.terminalOutput}> (Epoch=1, Node=pod-01)</span>
            <br />
            <span className={styles.terminalOutput}>[INFO] Registering job: cleanup-temp-files cron=&quot;0 */6 * * *&quot;</span>
            <br />
            <span className={styles.terminalOutput}>[INFO] Triggering job: cleanup-temp-files [epoch=1] [duration=42ms] [status=SUCCESS]</span>
          </div>
        </div>
      </div>
    </header>
  );
}

const featuresData = [
  {
    icon: (
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
        <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
        <line x1="6" y1="6" x2="6.01" y2="6" />
        <line x1="6" y1="18" x2="6.01" y2="18" />
      </svg>
    ),
    title: 'Zero New Infrastructure',
    description: 'Uses standard PostgreSQL session-bound advisory locks. No Redis clusters, no etcd, no sidecars, and no extra deployment daemons.',
  },
  {
    icon: (
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 2L2 7l10 5 10-5-10-5z" />
        <path d="M2 17l10 5 10-5" />
        <path d="M2 12l10 5 10-5" />
      </svg>
    ),
    title: 'Single Leader Scheduler',
    description: 'Only 1 active Leader node runs the timer clock across all scaled container replicas. Zero database thundering-herd lock spikes.',
  },
  {
    icon: (
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
      </svg>
    ),
    title: 'Split-Brain Fencing Tokens',
    description: 'Passes strictly increasing monotonic LeaderEpoch tokens in job context to fence stale database writes during network partitions.',
  },
  {
    icon: (
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
        <polyline points="14 2 14 8 20 8" />
        <line x1="12" y1="18" x2="12" y2="12" />
        <line x1="9" y1="15" x2="15" y2="15" />
      </svg>
    ),
    title: 'Zero Migration Default',
    description: 'Advisory locks live in Postgres memory structures. Default operation requires zero database tables and zero DDL migrations.',
  },
  {
    icon: (
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <polyline points="16 18 22 12 16 6" />
        <polyline points="8 6 2 12 8 18" />
      </svg>
    ),
    title: 'In-Process Go Functions',
    description: 'Register standard Go functions directly with scheduler.Add. No JSON payload serialization or worker queue decoupling required.',
  },
  {
    icon: (
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <line x1="18" y1="20" x2="18" y2="10" />
        <line x1="12" y1="20" x2="12" y2="4" />
        <line x1="6" y1="20" x2="6" y2="14" />
      </svg>
    ),
    title: 'Built-in Observability & UI',
    description: 'Embedded Web Dashboard UI, Prometheus metrics exporter, OpenTelemetry distributed tracing, and REST admin API out of the box.',
  },
];

function FeatureCard({icon, title, description}: {icon: ReactNode; title: string; description: string}) {
  return (
    <div className="col col--4" style={{marginBottom: '1.5rem'}}>
      <div className={styles.featureCard}>
        <div className={styles.featureIcon}>
          {icon}
        </div>
        <h3 className={styles.featureTitle}>{title}</h3>
        <p className={styles.featureDesc}>{description}</p>
      </div>
    </div>
  );
}

export default function Home(): ReactNode {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`${siteConfig.title} | Distributed Cron Scheduler for Go`}
      description="d-cron is a distributed cron scheduler library for Go powered by PostgreSQL advisory locks. Fast, lightweight, zero extra tables, and 100% open source.">
      <HomepageHeader />
      <main>
        <section className={styles.features}>
          <div className="container">
            <div className={styles.sectionHeader}>
              <Heading as="h2" className={styles.sectionTitle}>
                Everything you need for distributed job scheduling
              </Heading>
              <p className={styles.sectionSubtitle}>
                A complete production-ready cron engine designed specifically for scaled Go applications.
              </p>
            </div>
            <div className="row">
              {featuresData.map((feature, idx) => (
                <FeatureCard key={idx} {...feature} />
              ))}
            </div>
          </div>
        </section>
      </main>
      <section className={styles.cta}>
        <div className={styles.ctaInner}>
          <Heading as="h2" className={styles.ctaTitle}>
            Ready to simplify your background jobs?
          </Heading>
          <p className={styles.ctaSubtitle}>
            Get started in under 2 minutes. Install d-cron into your Go project today.
          </p>
          <Link className={styles.ctaButton} to="/docs/intro">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="5" y1="12" x2="19" y2="12" />
              <polyline points="12 5 19 12 12 19" />
            </svg>
            Read Documentation
          </Link>
        </div>
      </section>
    </Layout>
  );
}
