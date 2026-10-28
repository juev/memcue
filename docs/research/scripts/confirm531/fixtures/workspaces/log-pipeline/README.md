# log-pipeline

Configuration of the Marrowlog agents that ship application logs to the central collector.

- Agents run as a sidecar on each application VM (1 vCPU reserved for the agent).
- Peak volume: 50,000 lines per second per agent; lines average about 900 bytes (JSON with stack traces).
- Today the agents talk to the collector over plain TCP. Security review requires TLS to the collector by the end of
  the next quarter.
