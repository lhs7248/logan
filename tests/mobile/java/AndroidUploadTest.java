import com.dianping.logan.Logan;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.file.Files;
import java.util.Arrays;
import java.util.concurrent.atomic.AtomicBoolean;

public class AndroidUploadTest {
    static void check(boolean value) { if (!value) throw new AssertionError("upload mismatch"); }
    public static void main(String[] args) throws Exception {
        byte[] original = new byte[] {1, 0, 2, (byte)255, 0, 3};
        Logan.fixture = Files.createTempFile("synthetic-clogan", ".bin").toFile();
        Files.write(Logan.fixture.toPath(), original);
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        AtomicBoolean received = new AtomicBoolean();
        server.createContext("/logan-web/api/workbench/v1/sdk/native", exchange -> {
            try {
                check(exchange.getRequestMethod().equals("POST"));
                String[][] expected = {{"appId","app"},{"deviceId","device"},{"platform","1"},
                    {"fileDate","2026-10-22"},{"appVersion","1.2"},{"buildVersion","42"},
                    {"X-Logan-Upload-Token","synthetic-upload-only"},{"X-Logan-Upload-ID","retry-1"}};
                boolean unknownVersion = exchange.getRequestURI().getPath().endsWith("/unknown");
                for (String[] h : expected) {
                    if (unknownVersion && (h[0].equals("appVersion") || h[0].equals("buildVersion"))) {
                        check(exchange.getRequestHeaders().getFirst(h[0]) == null);
                    } else check(h[1].equals(exchange.getRequestHeaders().getFirst(h[0])));
                }
                check(Arrays.equals(original, exchange.getRequestBody().readAllBytes()));
                received.set(true);
                byte[] reply = "{\"code\":200}".getBytes();
                exchange.sendResponseHeaders(200, reply.length);
                exchange.getResponseBody().write(reply);
            } finally { exchange.close(); }
        });
        server.start();
        try {
            AtomicBoolean callback = new AtomicBoolean();
            WorkbenchLoganUpload.upload("http://127.0.0.1:" + server.getAddress().getPort() +
                "/logan-web/api/workbench/v1/sdk/native", "2026-10-22", "app", "device", "1.2", "42",
                "synthetic-upload-only", "retry-1", (status, data) -> callback.set(status == 200));
            check(received.get()); check(callback.get());
            received.set(false); callback.set(false);
            WorkbenchLoganUpload.upload("http://127.0.0.1:" + server.getAddress().getPort() +
                "/logan-web/api/workbench/v1/sdk/native/unknown", "2026-10-22", "app", "device", null, "",
                "synthetic-upload-only", "retry-1", (status, data) -> callback.set(status == 200));
            check(received.get()); check(callback.get());
            for (String[] versions : new String[][] {{"bad\rversion", null}, {null, "bad\nbuild"}}) {
                boolean rejected = false;
                try {
                    WorkbenchLoganUpload.upload("http://127.0.0.1", "2026-10-22", "app", "device", versions[0], versions[1],
                        "synthetic-upload-only", null, null);
                } catch (IllegalArgumentException expected) { rejected = true; }
                check(rejected);
            }
            System.out.println("Android helper + actual sender loopback: passed");
        } finally { server.stop(0); Files.deleteIfExists(Logan.fixture.toPath()); }
    }
}
