FROM python:3.13-alpine
WORKDIR /provider
COPY tests/e2e/provider/server.py /provider/server.py
COPY tests/e2e/provider/run_control.py /provider/run_control.py
COPY tests/e2e/provider/authorization.py /provider/authorization.py
COPY tests/e2e/provider/compaction.py /provider/compaction.py
COPY tests/e2e/provider/collaboration.py /provider/collaboration.py
COPY tests/e2e/provider/recovery.py /provider/recovery.py
COPY tests/e2e/provider/knowledge_tools.py /provider/knowledge_tools.py
EXPOSE 8099
ENTRYPOINT ["python", "/provider/server.py"]
