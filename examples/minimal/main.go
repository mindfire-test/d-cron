// Command minimal demonstrates the most compact d-cron integration.
//
// Intended purpose (implemented once Phase 1 lands):
//
//	package main
//
//	func main() {
//	    db, _ := sql.Open("pgx", "...")
//	    sched, _ := dcron.New(db, dcron.WithSessionStableConnection())
//
//	    sched.Add("send-invoices", "0 2 * * *", func(ctx context.Context) error {
//	        return billing.SendInvoices(ctx)
//	    })
//
//	    ctx := context.Background()
//	    sched.Start(ctx)
//	    defer sched.Stop(ctx)
//	}
//
// Placeholder for now; the scheduler core is not yet implemented.
package main

func main() {}
