FROM python@sha256:2d9aefe2fef018a7eb2c13064c89c71929800fd2e5dccdbf52ea5da5bb8d929a

ARG WORKER_SHA256
LABEL aim.worker.sha256="${WORKER_SHA256}"

COPY --chmod=0444 worker.py /opt/aim/worker.py
RUN test "${#WORKER_SHA256}" -eq 64 \
    && printf '%s  /opt/aim/worker.py\n' "$WORKER_SHA256" | sha256sum -c - \
    && chmod 0555 /opt/aim \
    && mkdir -p /workspace \
    && chmod 0555 /workspace

USER 65532:65532
WORKDIR /workspace
ENTRYPOINT ["python3", "-I", "-B", "/opt/aim/worker.py"]
