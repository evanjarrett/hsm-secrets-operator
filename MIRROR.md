Plan: Optimize Mirror Reconciliation to Event-Driven Model

     Current Problem

     The mirror manager (internal/modes/manager/runnable.go:90) runs MirrorAllSecrets() every 30 seconds regardless of actual changes. This is wasteful because:
     - API operations already handle mirroring directly (WriteSecret/DeleteSecret in proxy_client.go)
     - Agent deployments are stable most of the time
     - Direct HSM modifications outside the operator are rare

     Current Mirror Triggers

     1. Every 30 seconds - Blanket sync via ticker in runnable.go
     2. API operations - Already mirror to all devices (WriteSecret/DeleteSecret)
     3. No trigger on agent changes - New agents don't trigger immediate sync

     Proposed Solution: Event-Driven Mirroring

     1. Replace Timer with Event-Based Triggers

     File: internal/modes/manager/runnable.go
     - Remove the 30-second ticker
     - Add channel-based event system for mirror triggers
     - Keep periodic sync as safety net (configurable, default 5-10 minutes)

     2. Add Mirror Trigger Events

     Trigger immediate mirroring when:
     - New agent pod becomes ready (agent added to pool)
     - Agent pod recreated (after migration/restart)  
     - Manual trigger via API (new endpoint for force sync)
     - Configurable periodic safety sync (default 5 minutes instead of 30 seconds)

     3. Implementation Changes

     3.1 Modify MirrorManagerRunnable
     type MirrorManagerRunnable struct {
         // existing fields...
         mirrorTrigger chan MirrorTriggerEvent
     }

     type MirrorTriggerEvent struct {
         Reason string // "agent_ready", "manual", "periodic"
         Force  bool   // Skip optimization checks
     }

     3.2 Watch Agent Pod Events
     - Add pod informer to watch for agent pods becoming ready
     - Trigger mirror when new agent with label app.kubernetes.io/component=hsm-agent becomes ready
     - Debounce rapid changes (wait 5 seconds for multiple agents)

     3.3 Add Manual Trigger API
     - New endpoint: POST /api/v1/mirror/sync
     - Allows operators to manually trigger sync when needed
     - Useful after direct HSM modifications

     3.4 Configurable Intervals
     mirrorSync:
       periodicInterval: 5m    # Safety net sync (was 30s)
       debounceWindow: 5s      # Wait for multiple agent changes
       enabled: true           # Can disable periodic sync entirely

     4. Optimization Logic

     Skip sync if nothing changed:
     - Track last successful sync timestamp
     - Track agent pod generation/UIDs
     - If no new agents and < periodicInterval, skip sync
     - Log at debug level when skipping

     Smart sync on agent changes:
     - When new agent detected, only sync TO that agent
     - Don't re-sync between existing stable agents
     - Use partial sync instead of full MirrorAllSecrets

     5. Benefits

     - Reduced load: From 120 syncs/hour to ~12 (90% reduction)
     - Faster response: Immediate sync when agents change
     - Better observability: Clear trigger reasons in logs
     - Configurable: Can tune or disable based on needs
     - Backward compatible: Still handles all scenarios

     6. Testing Scenarios

     1. Agent addition: New agent should trigger sync within 5 seconds
     2. Agent migration: Migrated agent should get synced immediately
     3. API operations: Should continue to mirror directly
     4. Manual trigger: API endpoint should force immediate sync
     5. Periodic safety: Should still sync after configured interval

     7. Implementation Order

     1. Add event channel and trigger types
     2. Implement pod watcher for agent events
     3. Replace ticker with event loop
     4. Add manual trigger API endpoint
     5. Add configuration for intervals
     6. Test with multiple agent scenarios

     This approach maintains data consistency while dramatically reducing unnecessary reconciliation overhead.
