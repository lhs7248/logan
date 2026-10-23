import com.dianping.logan.Logan;
import com.sun.net.httpserver.HttpsConfigurator;
import com.sun.net.httpserver.HttpsServer;
import java.io.InputStream;
import java.net.InetSocketAddress;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.util.concurrent.atomic.AtomicInteger;
import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.TrustManagerFactory;

/** Real JSSE handshake with a temporary trusted certificate valid only for localhost. */
public class AndroidUploadTlsTest {
    static void check(boolean value, String reason) { if (!value) throw new AssertionError(reason); }
    static int upload(String endpoint) {
        AtomicInteger status = new AtomicInteger(-999);
        WorkbenchLoganUpload.upload(endpoint, "2026-10-22", "app", "device", "1.2", "42",
            "synthetic-upload-only", null, (code, data) -> status.set(code));
        return status.get();
    }
    public static void main(String[] args) throws Exception {
        KeyStore keys = KeyStore.getInstance("PKCS12");
        try (InputStream input = Files.newInputStream(Path.of(args[0]))) {
            keys.load(input, "synthetic-test-password".toCharArray());
        }
        KeyManagerFactory keyManager = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        keyManager.init(keys, "synthetic-test-password".toCharArray());
        TrustManagerFactory trustManager = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        trustManager.init(keys);
        SSLContext tls = SSLContext.getInstance("TLS");
        tls.init(keyManager.getKeyManagers(), trustManager.getTrustManagers(), null);
        SSLSocketFactory original = HttpsURLConnection.getDefaultSSLSocketFactory();
        // Trust this fixture certificate, but preserve the platform's hostname verification.
        HttpsURLConnection.setDefaultSSLSocketFactory(tls.getSocketFactory());
        Logan.fixture = Files.createTempFile("synthetic-clogan-tls", ".bin").toFile();
        Files.write(Logan.fixture.toPath(), new byte[] {0, 1, 2, (byte)255});
        HttpsServer server = HttpsServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.setHttpsConfigurator(new HttpsConfigurator(tls));
        AtomicInteger requests = new AtomicInteger();
        server.createContext("/intake", exchange -> {
            requests.incrementAndGet();
            exchange.getRequestBody().readAllBytes();
            exchange.sendResponseHeaders(200, 0);
            exchange.close();
        });
        server.start();
        try {
            int port = server.getAddress().getPort();
            check(upload("https://localhost:" + port + "/intake") == 200, "matching certificate rejected");
            check(requests.get() == 1, "matching upload missing");
            check(upload("https://127.0.0.1:" + port + "/intake") == -1, "mismatched hostname accepted");
            check(requests.get() == 1, "mismatched host received credential-bearing HTTP request");
            System.out.println("Android actual sender TLS hostname verification: passed");
        } finally {
            server.stop(0);
            HttpsURLConnection.setDefaultSSLSocketFactory(original);
            Files.deleteIfExists(Logan.fixture.toPath());
        }
    }
}
