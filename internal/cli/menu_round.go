package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// roundOfferQuestion is the question of the mid-session offer: the resolvers ran out after
// a full round of failed handshakes.
const roundOfferQuestion = "Check and edit resolvers? (y/N): "

// connectWithResolvers connects and, when a full round of handshakes fails after the
// tunnel has been up, stops the connection, offers to check and edit the resolvers and
// connects again with the saved list.
func (m *menu) connectWithResolvers(ctx context.Context, e *env, name string, abort <-chan struct{}) error {
	for {
		rounds := make(chan []supervisor.ResolverFailure, 1)
		connCtx, connCancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- m.connect(connCtx, e, connectRequest{
				profileName: name,
				onRoundFailed: func(failures []supervisor.ResolverFailure) {
					// The callback runs on the supervisor's Run goroutine: it must not
					// block, and a round nobody waits for is dropped.
					select {
					case rounds <- failures:
					default:
					}
				},
			}, abort)
		}()

		select {
		case err := <-done:
			connCancel()
			return err
		case failures := <-rounds:
			// The question must not be printed over the status line: the connection is
			// stopped first, and the status line clears its row as it goes.
			connCancel()
			err := <-done
			if ctx.Err() != nil {
				return err // a stop by the person: the stop line has answered for it
			}
			yes, err := m.offerRoundFailed(ctx, e, failures)
			if err != nil || !yes {
				return err
			}
			if err := m.checkResolversItem(ctx, e, nil); err != nil {
				var ec *exitCodeError
				if !errors.As(err, &ec) {
					return err
				}
			}
			if err := m.editResolvers(ctx, e, name); err != nil {
				return err
			}
		}
	}
}

// offerRoundFailed prints the failed resolvers of the round and asks whether to check and
// edit them. The names are the supervisor's, the ones the event carries: the check is not
// run again to get them. The end of input is errCancelled, which closes the menu.
func (m *menu) offerRoundFailed(ctx context.Context, e *env, failures []supervisor.ResolverFailure) (bool, error) {
	fmt.Fprintln(e.stdout, "No resolver completed a handshake in a full round:")
	for _, failure := range failures {
		fmt.Fprintf(e.stdout, "  %s: %v\n", failure.Resolver, failure.Err)
	}
	answer, err := m.ask.Ask(ctx, roundOfferQuestion)
	if err != nil {
		return false, readError(ctx, err)
	}
	return isYes(answer), nil
}
