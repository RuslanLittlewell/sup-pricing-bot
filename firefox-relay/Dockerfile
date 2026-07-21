FROM python:3.12-bookworm

RUN pip install --no-cache-dir playwright==1.54.0 \
    && playwright install --with-deps firefox

WORKDIR /app
COPY server.py /app/server.py

EXPOSE 8080
CMD ["python", "/app/server.py"]
