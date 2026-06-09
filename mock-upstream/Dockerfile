FROM python:3.12-alpine

WORKDIR /app
COPY server.py /app/server.py

# Stdlib only — no pip install needed.
EXPOSE 8080

# Buffered stderr is annoying when reading docker logs.
ENV PYTHONUNBUFFERED=1

CMD ["python3", "/app/server.py"]
