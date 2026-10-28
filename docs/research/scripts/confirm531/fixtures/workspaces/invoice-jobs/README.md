# invoice-jobs

Background jobs that render and send invoices. We are evaluating Ostrel as the job queue; the vendor documentation we
rely on is copied under `docs/vendor/` so that reviews do not depend on the vendor's site.

Invoice payloads (the rendered invoice plus metadata) are up to 300 KB.
