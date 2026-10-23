package test.logan.dianping.com.logan;

import android.app.Application;
import com.dianping.logan.Logan;
import com.dianping.logan.LoganConfig;
import org.json.JSONObject;
import java.io.File;
import java.io.FileInputStream;
import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;

public final class WorkbenchApplication extends Application {
    WorkbenchProfile profile;
    final WorkbenchSubmission submissions=new WorkbenchSubmission();
    String initializationError;
    @Override public void onCreate() {
        super.onCreate();
        File file=new File(getFilesDir(), "workbench-profile.json");
        if (!file.isFile()) {initializationError="Private profile missing. Provision files/workbench-profile.json, then relaunch.";return;}
        try {
            if(file.length()>8192) throw new IllegalArgumentException("Private profile exceeds size limit");
            ByteArrayOutputStream out=new ByteArrayOutputStream();
            try(FileInputStream in=new FileInputStream(file)){byte[] buffer=new byte[1024];int n;while((n=in.read(buffer))!=-1){out.write(buffer,0,n);if(out.size()>8192)throw new IllegalArgumentException("Private profile exceeds size limit");}}
            profile=WorkbenchProfile.parse(new JSONObject(new String(out.toByteArray(), StandardCharsets.UTF_8)));
            if(!BuildConfig.APPLICATION_ID.equals(profile.appId))throw new IllegalArgumentException("Profile application differs from installed application");
            File cache=new File(getFilesDir(),"logan-cache"), logs=new File(getFilesDir(),"logan-workbench");
            if ((!cache.isDirectory() && !cache.mkdirs()) || (!logs.isDirectory() && !logs.mkdirs())) throw new IllegalArgumentException("Private log directories unavailable");
            Logan.setDebug(false);
            Logan.init(new LoganConfig.Builder().setCachePath(new File(getFilesDir(),"logan-cache").getAbsolutePath())
                    .setPath(new File(getFilesDir(),"logan-workbench").getAbsolutePath()).setEncryptKey16(profile.key).setEncryptIV16(profile.iv).build());
        } catch (Exception e) {profile=null; initializationError="Private profile or native initialization failed. Validate profile fields and 16-byte key/IV, then relaunch.";}
    }
}
