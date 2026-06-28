FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY nip50_proxy ./nip50_proxy

EXPOSE 7781

CMD ["python", "-m", "nip50_proxy"]
