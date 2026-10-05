FROM python:3.13-alpine
WORKDIR /provider
COPY tests/e2e/provider/server.py /provider/server.py
EXPOSE 8099
ENTRYPOINT ["python", "/provider/server.py"]
