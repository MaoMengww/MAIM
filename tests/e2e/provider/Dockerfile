FROM python:3.13-alpine
WORKDIR /provider
COPY tests/e2e/provider/server.py /provider/server.py
COPY tests/e2e/provider/run_control.py /provider/run_control.py
COPY tests/e2e/provider/authorization.py /provider/authorization.py
COPY tests/e2e/provider/compaction.py /provider/compaction.py
EXPOSE 8099
ENTRYPOINT ["python", "/provider/server.py"]
